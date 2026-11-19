package network

import (
	"errors"
	"net"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/trace"
)

type failingCloseConnection struct {
	net.Conn
	calls int
	cause error
}

func (c *failingCloseConnection) Close() error { c.calls++; return c.cause }
func TestDisconnectReportsFailureOnce(t *testing.T) {
	cause := errors.New("synthetic close failure")
	owned := &failingCloseConnection{cause: cause}
	session := NewSession(&configurations.ConnectionConfig{}, trace.NilTracer())
	session.conn = owned
	if err := session.DisconnectWithError(); !errors.Is(err, cause) {
		t.Fatalf("lost close cause: %v", err)
	}
	if session.conn != nil || session.sslConn != nil {
		t.Fatal("closed transport still owned")
	}
	if err := session.DisconnectWithError(); err != nil || owned.calls != 1 {
		t.Fatalf("repeated close: calls=%d err=%v", owned.calls, err)
	}
}
