package rtconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	igoctx "github.com/igolaizola/context"
)

// Dialer is a dialer based on a http.RoundTripper
type Dialer struct {
	// Transport specifies the mechanism by which HTTP POST request is made.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// Timeout specifies a time limit for dialing.
	// A Timeout of zero means no timeout.
	Timeout time.Duration
}

// Dial creates a net.Conn based on a POST request within the http.RoundTripper
func (d *Dialer) Dial(parent context.Context, addr string, header http.Header) (net.Conn, error) {
	// Get transport
	transport := d.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	// Pipe to redirect conn writer into request.Body
	reader, writer := io.Pipe()

	// Create request
	req, err := http.NewRequest("POST", addr, reader)
	if err != nil {
		return nil, err
	}
	req.Header = header
	ctx, cancel := context.WithCancel(parent)
	req = req.WithContext(ctx)

	// Timer to control request timeout
	timer := context.Background()
	stop := context.CancelFunc(func() {})
	if d.Timeout > 0 {
		timer, stop = context.WithTimeout(ctx, d.Timeout)
		go func() {
			<-timer.Done()
			if timer.Err() == context.DeadlineExceeded {
				cancel()
			}
		}()
	}

	// Launch request
	resp, err := transport.RoundTrip(req)
	stop()
	if err == context.Canceled && timer.Err() == context.DeadlineExceeded {
		cancel()
		return nil, timeoutError(fmt.Sprintf("rtconn: request to %s timed out", addr))
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		return nil, fmt.Errorf("rtconn: http error %d", resp.StatusCode)
	}

	// Create roundtrip conn
	rdCtx, _ := igoctx.WithDeadline(ctx)
	wrCtx, _ := igoctx.WithDeadline(ctx)
	conn := &roundTripConn{
		addr:          addr,
		reader:        resp.Body,
		writer:        writer,
		context:       ctx,
		cancel:        cancel,
		readDeadline:  rdCtx,
		writeDeadline: wrCtx,
		readLock:      &sync.Mutex{},
	}
	return conn, nil
}

// roundTripConn is a net.Conn implementation
type roundTripConn struct {
	addr          string
	reader        io.ReadCloser
	writer        io.WriteCloser
	context       context.Context
	cancel        context.CancelFunc
	readDeadline  igoctx.Context
	writeDeadline igoctx.Context
	readChan      chan readResult
	readLock      *sync.Mutex
}

type readResult struct {
	n    int
	err  error
	data []byte
}

// Read implements net.Conn.Read
func (r *roundTripConn) Read(b []byte) (int, error) {
	// Multiple concurrent calls to Read get blocked
	r.readLock.Lock()
	defer r.readLock.Unlock()

	// Launch inner read call if needed
	if r.readChan == nil {
		r.readChan = make(chan readResult)
		go func() {
			n, err := r.reader.Read(b)
			data := make([]byte, n)
			copy(data, b[:n])
			r.readChan <- readResult{n, err, data}
		}()
	}

	// Wait for data or deadline
	select {
	case read := <-r.readChan:
		n := read.n
		if n > len(b) {
			n = len(b)
			// Add exceeding data to read channel
			data := make([]byte, read.n-n)
			copy(data[0:read.n-n], read.data[n:read.n])
			go func() {
				r.readChan <- readResult{read.n - n, read.err, data}
			}()
		} else {
			// Reset read channel
			r.readChan = nil
		}
		copy(b[:n], read.data[:n])
		return n, read.err
	case <-r.readDeadline.Done():
		// WARNING! conn.Close must be called in order to unblock internal goroutine
		return 0, timeoutError("rtconn: read timed out")
	case <-r.context.Done():
		return 0, r.context.Err()
	}
}

type writeResult struct {
	n   int
	err error
}

// Write implements net.Conn.Write
func (r *roundTripConn) Write(b []byte) (int, error) {
	done := make(chan writeResult)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		n, err := r.writer.Write(b)
		select {
		case <-quit:
		case done <- writeResult{n, err}:
		}
		close(done)
	}()

	select {
	case r := <-done:
		return r.n, r.err
	case <-r.writeDeadline.Done():
		// WARNING! conn.Close must be called in order to unblock internal goroutine
		return 0, timeoutError("rtconn: write timed out")
	case <-r.context.Done():
		return 0, r.context.Err()
	}
}

// Close implements net.Conn.Close
func (r *roundTripConn) Close() error {
	if r.cancel != nil {
		r.cancel()
	}
	if r.writer != nil {
		_ = r.writer.Close()
	}
	if r.reader != nil {
		return r.reader.Close()
	}
	return nil
}

// LocalAddr implements net.Conn.LocalAddr
func (r *roundTripConn) LocalAddr() net.Addr {
	return addr("")
}

// RemoteAddr implements net.Conn.RemoteAddr
func (r *roundTripConn) RemoteAddr() net.Addr {
	return addr(r.addr)
}

// SetDeadline implements net.Conn.SetDeadline
func (r *roundTripConn) SetDeadline(t time.Time) error {
	err1 := r.SetReadDeadline(t)
	err2 := r.SetWriteDeadline(t)
	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}
	return nil
}

// SetReadDeadline implements net.Conn.SetReadDeadline
func (r *roundTripConn) SetReadDeadline(t time.Time) error {
	if err := r.context.Err(); err != nil {
		return err
	}
	err := r.readDeadline.SetDeadline(t)
	if errors.Is(err, context.DeadlineExceeded) {
		r.readDeadline, _ = igoctx.WithDeadline(r.context)
		return r.readDeadline.SetDeadline(t)
	}
	return err
}

// SetWriteDeadline implements net.Conn.SetWriteDeadline
func (r *roundTripConn) SetWriteDeadline(t time.Time) error {
	if err := r.context.Err(); err != nil {
		return err
	}
	err := r.writeDeadline.SetDeadline(t)
	if errors.Is(err, context.DeadlineExceeded) {
		r.writeDeadline, _ = igoctx.WithDeadline(r.context)
		return r.writeDeadline.SetDeadline(t)
	}
	return err
}

// addr is a net.Addr implementation
type addr string

func (a addr) Network() string {
	return ""
}

func (a addr) String() string {
	return string(a)
}

// timeoutError is a net.Error implementation
type timeoutError string

func (t timeoutError) Error() string {
	return string(t)
}

func (timeoutError) Temporary() bool {
	return false
}
func (timeoutError) Timeout() bool {
	return true
}
