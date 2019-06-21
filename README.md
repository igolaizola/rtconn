[![Go Report Card](https://goreportcard.com/badge/github.com/igolaizola/rtconn)](https://goreportcard.com/report/github.com/igolaizola/rtconn)
[![GoDoc](https://godoc.org/github.com/igolaizola/rtconn/go?status.svg)](https://godoc.org/github.com/igolaizola/rtconn)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

# rtconn
golang package to create a net.Conn based on a POST request and a given http.RoundTripper

## usage

Example of creating a net.Conn from a HTTP2 request

```go
package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/igolaizola/rtconn"
)

func main() {
	// Dial to http2.golang.org example server
	dialer := rtconn.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.Dial(context.Background(), "https://http2.golang.org/reqinfo", nil)
	if err != nil {
		panic(err)
	}

	// Print received data until EOF or error
	data := make([]byte, 1024)
	for {
		n, err := conn.Read(data)
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		fmt.Printf(string(data[:n]))
	}

	// Close connection
	if err := conn.Close(); err != nil {
		panic(err)
	}
}

```
