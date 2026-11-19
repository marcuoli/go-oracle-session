package go_ora

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
)

type cleanupTransport struct{ *bytes.Reader }

func (c *cleanupTransport) Write(p []byte) (int, error)      { return len(p), nil }
func (c *cleanupTransport) Close() error                     { return nil }
func (c *cleanupTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *cleanupTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *cleanupTransport) SetDeadline(time.Time) error      { return nil }
func (c *cleanupTransport) SetReadDeadline(time.Time) error  { return nil }
func (c *cleanupTransport) SetWriteDeadline(time.Time) error { return nil }

type cleanupDialer struct{ transport net.Conn }

func (d cleanupDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.transport, nil
}

func cleanupStatement(t *testing.T) (*defaultStmt, *Connection) {
	t.Helper()
	packet := make([]byte, 32)
	binary.BigEndian.PutUint16(packet, uint16(len(packet)))
	packet[4] = 2
	binary.BigEndian.PutUint16(packet[8:], 312)
	binary.BigEndian.PutUint16(packet[12:], 8192)
	binary.BigEndian.PutUint16(packet[14:], 8192)
	binary.BigEndian.PutUint16(packet[20:], 32)
	config, err := ParseConfig("oracle://fixture:fixture@fixture:1521/service")
	if err != nil {
		t.Fatal(err)
	}
	config.Dialer = cleanupDialer{&cleanupTransport{bytes.NewReader(packet)}}
	session := network.NewSession(config, trace.NilTracer())
	if err := session.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	conn := &Connection{State: Opened, session: session, tracer: trace.NilTracer(), connOption: config}
	return &defaultStmt{connection: conn, cursorID: 1}, conn
}

type cleanupPool struct {
	t               *testing.T
	connects, execs int
	last            *Connection
}

func (p *cleanupPool) Driver() driver.Driver { return cleanupDriver{} }
func (p *cleanupPool) Connect(context.Context) (driver.Conn, error) {
	p.connects++
	stmt, native := cleanupStatement(p.t)
	p.last = native
	return &cleanupPoolConn{pool: p, stmt: stmt, native: native}, nil
}

type cleanupDriver struct{}

func (cleanupDriver) Open(string) (driver.Conn, error) { return nil, errors.New("connector required") }

type cleanupPoolConn struct {
	pool   *cleanupPool
	stmt   *defaultStmt
	native *Connection
}

func (c *cleanupPoolConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (c *cleanupPoolConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c *cleanupPoolConn) Close() error                        { return nil }
func (c *cleanupPoolConn) IsValid() bool                       { return !c.native.bad }
func (c *cleanupPoolConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	c.pool.execs++ // Execution succeeded; only the real native cursor close fails.
	return nil, c.stmt.Close()
}
func TestSuccessfulExecutionCleanupCannotRequestReplay(t *testing.T) {
	fixture := &cleanupPool{t: t}
	pool := sql.OpenDB(fixture)
	defer pool.Close()
	_, err := pool.ExecContext(t.Context(), "synthetic completed command")
	if !errors.Is(err, io.EOF) || errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("lost original cleanup cause or permitted retry: %v", err)
	}
	if fixture.execs != 1 || fixture.connects != 1 || !fixture.last.bad || pool.Stats().Idle != 0 {
		t.Fatalf("execs=%d connects=%d bad=%v idle=%d", fixture.execs, fixture.connects, fixture.last.bad, pool.Stats().Idle)
	}
}
