package session

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Proxy forwards loopback TCP from one port to another until the context
// ends. It exists so a compose app, which publishes whatever port its
// variant drew, reaches the candidate on the one fixed port the fronting
// proxy and the printed URL both name.
//
// A connection whose target refuses is closed rather than retried: the app
// is down for much of an interview by design, and a browser that gets a
// closed connection shows the failure the candidate is meant to see.
func Proxy(ctx context.Context, from, to int, out io.Writer) error {
	if from == to {
		return fmt.Errorf("proxy from and to are both %d", from)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", from)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "proxying %s to 127.0.0.1:%d\n", addr, to)

	// Closing the listener is what unblocks Accept, so the context needs a
	// goroutine rather than a deadline.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		_ = ln.Close()
	}()
	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go forward(conn, to)
	}
}

// forward pairs one accepted connection with a fresh dial to the target.
func forward(client net.Conn, to int) {
	defer func() { _ = client.Close() }()
	target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", to), 5*time.Second)
	if err != nil {
		return
	}
	defer func() { _ = target.Close() }()

	done := make(chan struct{}, 2)
	pipe := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		// Half-close so the peer sees the end of the body rather than waiting
		// on a connection neither side will write to again.
		if cw, ok := dst.(*net.TCPConn); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go pipe(target, client)
	go pipe(client, target)
	<-done
	<-done
}
