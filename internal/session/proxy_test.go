package session

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// freePort returns a loopback port nothing is listening on.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	for range 100 {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %d", port)
}

// The proxy carries a real request and its body, since that is all it has
// to do: the candidate's browser reaching the app on a fixed port.
func TestProxyCarriesHTTP(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello from %s", r.URL.Path)
	}))
	defer app.Close()
	target, err := strconv.Atoi(strings.TrimPrefix(app.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	front := freePort(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errs := make(chan error, 1)
	go func() { errs <- Proxy(ctx, front, target, io.Discard) }()
	waitForPort(t, front)

	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/products", front))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "hello from /products" {
		t.Errorf("body = %q", got)
	}

	// The context is what stops it, and it has to stop cleanly: this runs as
	// a session process and stop kills it.
	cancel()
	select {
	case err := <-errs:
		if err != nil {
			t.Errorf("proxy returned %v on shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("proxy did not stop with its context")
	}
}

// The app is down for much of an interview by design, so the proxy has to
// survive the target being gone and serve again when it comes back.
func TestProxySurvivesTheTargetGoingAway(t *testing.T) {
	target := freePort(t)
	front := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Proxy(ctx, front, target, io.Discard) }()
	waitForPort(t, front)

	// Nothing behind it yet: the connection is accepted and closed, which is
	// the failure the candidate is meant to see.
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", front)); err == nil {
		t.Error("got a response with no app behind the proxy")
	}

	app := &http.Server{
		Addr:              fmt.Sprintf("127.0.0.1:%d", target),
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "back") }),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = app.ListenAndServe() }()
	defer func() { _ = app.Close() }()
	waitForPort(t, target)

	res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", front))
	if err != nil {
		t.Fatalf("proxy did not recover when the app came back: %v", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "back" {
		t.Errorf("body = %q", body)
	}
}

func TestProxyRefusesOnePort(t *testing.T) {
	if err := Proxy(context.Background(), AppPort, AppPort, io.Discard); err == nil {
		t.Error("proxied a port to itself")
	}
}
