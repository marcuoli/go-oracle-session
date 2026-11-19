package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernTypes "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

type lobPoolFixture struct {
	stream                                  *lobStreamFixture
	connects, execs, discards, normalCloses int
}

func (f *lobPoolFixture) Driver() driver.Driver { return &compatibilityDriver{} }
func (f *lobPoolFixture) Connect(context.Context) (driver.Conn, error) {
	f.connects++
	native := &modern.Connection{State: modern.Opened}
	health := &connectionHealth{discard: func() error { f.discards++; native.State = modern.Closed; return nil }}
	return &lobPoolConnection{modernDriverConnection: &modernDriverConnection{Connection: native, health: health}, owner: f}, nil
}

type lobPoolConnection struct {
	*modernDriverConnection
	owner *lobPoolFixture
}

func (c *lobPoolConnection) ExecContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	c.owner.execs++
	out := args[0].Value.(modern.Out).Dest.(*modernTypes.Clob)
	if err := out.SetValue("seed"); err != nil {
		return nil, err
	}
	out.SetBytes(nil)
	out.SetStreamer(c.owner.stream)
	return driver.RowsAffected(1), nil
}
func (c *lobPoolConnection) Close() error {
	return c.health.close(func() error { c.owner.normalCloses++; c.Connection.State = modern.Closed; return nil })
}

func TestFailedClobDoesNotReturnPhysicalConnectionToPool(t *testing.T) {
	for _, operation := range []string{"read", "free"} {
		t.Run(operation, func(t *testing.T) {
			cause := io.ErrUnexpectedEOF
			stream := &lobStreamFixture{data: []byte{0, 65}}
			if operation == "read" {
				stream.readErr = cause
			} else {
				stream.freeErr = cause
			}
			fixture := &lobPoolFixture{stream: stream}
			pool := sql.OpenDB(fixture)
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			t.Cleanup(func() { pool.Close() })
			lease, err := pool.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var output Clob
			if _, err := lease.ExecContext(t.Context(), "fixture", Out{Dest: &output}); err != nil {
				t.Fatal(err)
			}
			err = output.Read(t.Context())
			if operation == "free" {
				if err != nil {
					t.Fatal(err)
				}
				err = output.Free()
			}
			if !errors.Is(err, cause) || errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("post-execution cause changed: %v", err)
			}
			_ = output.Free()
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
			second, err := pool.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			if fixture.connects != 2 || fixture.discards != 1 || fixture.execs != 1 {
				t.Fatalf("physical connects=%d discards=%d SQL dispatches=%d", fixture.connects, fixture.discards, fixture.execs)
			}
			if operation == "read" && stream.frees != 0 {
				t.Fatal("LOB protocol work after terminal discard")
			}
		})
	}
}

func TestTerminalCloseDisposesOnce(t *testing.T) {
	native := &modern.Connection{State: modern.Opened}
	discards := 0
	h := &connectionHealth{discard: func() error { discards++; native.State = modern.Closed; return nil }}
	connection := &modernDriverConnection{Connection: native, health: h}
	cause := errors.New("LOB failed")
	if err := h.fail(cause); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if connection.IsValid() {
		t.Fatal("failed physical connection still valid")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if discards != 1 {
		t.Fatalf("discard count=%d", discards)
	}
}
