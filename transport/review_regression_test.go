package transport_test

import (
	"context"
	"github.com/marcuoli/go-oracle-session/transport"
	"net"
	"testing"
	"time"
)

type reviewClosingConn struct {
	net.Conn
	entered, release chan struct{}
}

func (c *reviewClosingConn) Close() error { close(c.entered); <-c.release; return c.Conn.Close() }
func TestReviewConcurrentCloseJoinsPhysicalClosure(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c := &reviewClosingConn{Conn: left, entered: make(chan struct{}), release: make(chan struct{})}
	d, err := transport.WithDialer(time.Second, func(context.Context, string, string) (net.Conn, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.DialContext(context.Background(), "tcp", "example.invalid:1521"); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() { first <- d.Close() }()
	<-c.entered
	second := make(chan error, 1)
	go func() { second <- d.Close() }()
	var early bool
	select {
	case err := <-second:
		early = true
		if err != nil {
			t.Error(err)
		}
	case <-time.After(100 * time.Millisecond):
	}
	close(c.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if !early {
		<-second
	} else {
		t.Error("second Close returned success before the tracked physical connection finished closing")
	}
}
