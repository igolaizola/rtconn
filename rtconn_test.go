package rtconn

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func TestEcho(t *testing.T) {
	// Launch h2c server
	addr := "localhost:8154"
	go h2cServe(addr, "echo")

	// Dial
	dialer := Dialer{
		Transport: transport{},
		Timeout:   1 * time.Second,
	}
	conn, err := dialer.Dial(context.Background(), fmt.Sprintf("http://%s", addr), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Echo write read test
	if _, err := conn.Write([]byte("echo world")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 64)
	n, err := conn.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:n]) != "echo world" {
		t.Fatalf("Expected: echo world, got: %s", string(data[:n]))
	}

	// Close connection
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDialTimeout(t *testing.T) {
	// Launch h2c server
	addr := "localhost:8154"
	go h2cServe(addr, "dial-timeout")

	// Dial
	dialer := Dialer{
		Transport: transport{},
		Timeout:   50 * time.Millisecond,
	}
	_, err := dialer.Dial(context.Background(), fmt.Sprintf("http://%s", addr), nil)
	if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("Expected: timeout error, got: %v", err)
	}
}

func TestReadTimeout(t *testing.T) {
	// Launch h2c server
	addr := "localhost:8154"
	go h2cServe(addr, "read-timeout")

	// Dial
	dialer := Dialer{Transport: transport{}}
	conn, err := dialer.Dial(context.Background(), fmt.Sprintf("http://%s", addr), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Read
	data := make([]byte, 64)
	conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	_, err = conn.Read(data)
	if netErr, ok := err.(net.Error); !ok || !netErr.Timeout() {
		t.Fatalf("Expected: timeout error, got: %v", err)
	}

	// Close connection
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCancelContext(t *testing.T) {
	// Launch h2c server
	addr := "localhost:8154"
	go h2cServe(addr, "dial-timeout")

	// Dial
	dialer := Dialer{
		Transport: transport{},
		Timeout:   50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := dialer.Dial(ctx, fmt.Sprintf("http://%s", addr), nil)
	if err != context.Canceled {
		t.Fatalf("Expected: context canceled, got: %v", err)
	}
}

func h2cServe(addr string, h handler) {
	srv := &http.Server{
		Addr:    addr,
		Handler: h2c.NewHandler(h, &http2.Server{}),
	}
	_ = srv.ListenAndServe()
}

type handler string

func (h handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch h {
	case "echo":
		w.WriteHeader(http.StatusOK)
		flush(w)
		data := make([]byte, 64)
		n, err := r.Body.Read(data)
		if err != nil {
			return
		}
		if _, err := w.Write(data[:n]); err != nil {
			return
		}
		flush(w)
		<-r.Context().Done()
	case "dial-timeout":
		<-time.After(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		flush(w)
		<-r.Context().Done()
	case "read-timeout":
		w.WriteHeader(http.StatusOK)
		flush(w)
		<-time.After(100 * time.Millisecond)
		if _, err := w.Write([]byte("Sorry, I'm late")); err != nil {
			return
		}
		flush(w)
		<-r.Context().Done()
	}

}

func flush(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

type transport struct {
}

// RoundTrip implements http.Roundtripper.Roundtrip
func (transport) RoundTrip(req *http.Request) (*http.Response, error) {
	t := &http2.Transport{
		AllowHTTP: true,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}
	return t.RoundTrip(req)
}
