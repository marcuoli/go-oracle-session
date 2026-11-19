package network

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
)

type gatedReadTransport struct {
	net.Conn
	writing, closing, release chan struct{}
	calls                     atomic.Int32
	once                      sync.Once
	cause                     error
}

func (c *gatedReadTransport) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.writing) })
	return c.Conn.Write(p)
}
func (c *gatedReadTransport) Close() error {
	c.calls.Add(1)
	err := c.Conn.Close()
	close(c.closing)
	<-c.release
	return errors.Join(err, c.cause)
}
func TestLazyReadCancellationClosesOutsideProtocolLockAndJoins(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	cause := errors.New("synthetic transport close cause")
	transport := &gatedReadTransport{Conn: raw, writing: make(chan struct{}), closing: make(chan struct{}), release: make(chan struct{}), cause: cause}
	session := NewSession(&configurations.ConnectionConfig{}, trace.NilTracer())
	session.conn = transport
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	join := session.StartReadContext(ctx)
	written := make(chan error, 1)
	go func() { written <- session.writePacket(newMarkerPacket(marker_type_interrupt, session.Context)) }()
	select {
	case <-transport.writing:
	case <-time.After(2 * time.Second):
		t.Fatal("write did not block")
	}
	cancel()
	select {
	case <-transport.closing:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation waited for protocol mutex")
	}
	finished := make(chan error, 1)
	go func() { finished <- join() }()
	select {
	case <-finished:
		t.Fatal("read returned before transport callback joined")
	default:
	}
	close(transport.release)
	if err := <-finished; !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if err := <-written; err == nil {
		t.Fatal("blocked write was not interrupted")
	}
	if err := session.DisconnectWithError(); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if transport.calls.Load() != 1 || session.conn != nil {
		t.Fatalf("close calls=%d owned=%v", transport.calls.Load(), session.conn != nil)
	}
	if err := join(); !errors.Is(err, cause) {
		t.Fatal("join did not retain cause")
	}
}

func TestCompletedLazyReadStopsItsCancellationCallback(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	defer raw.Close()
	session := NewSession(&configurations.ConnectionConfig{}, trace.NilTracer())
	session.conn = raw
	ctx, cancel := context.WithCancel(t.Context())
	join := session.StartReadContext(ctx)
	if err := join(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if session.readInterrupt.fired {
		t.Fatal("completed read retained cancellation authority")
	}
}
