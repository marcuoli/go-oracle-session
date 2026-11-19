package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
)

type transactionDialer struct{}

func (transactionDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, io.EOF
}

type transactionPool struct {
	t                  *testing.T
	mode               Mode
	connects, discards atomic.Int32
}

func (f *transactionPool) Driver() driver.Driver { return &compatibilityDriver{} }
func (f *transactionPool) Connect(context.Context) (driver.Conn, error) {
	f.connects.Add(1)
	h := &connectionHealth{discard: func() error { f.discards.Add(1); return nil }}
	// Initialize the real native transaction owner without contacting a server.
	// The synthetic admission fails at the injected dialer, after tracer/session setup.
	if f.mode == V2 {
		cfg, err := legacy.ParseConfig("oracle://fixture:fixture@fixture:1521/service")
		if err != nil {
			return nil, err
		}
		cfg.Dialer = transactionDialer{}
		native, err := legacy.NewConnection("", cfg)
		if err != nil {
			return nil, err
		}
		if err := native.OpenWithContext(f.t.Context()); !errors.Is(err, io.EOF) {
			f.t.Fatalf("unexpected synthetic admission: %v", err)
		}
		native.State = legacy.Opened
		c := &legacyDriverConnection{Connection: native, health: h}
		if !c.IsValid() {
			f.t.Fatal("fixture starts invalid")
		}
		return c, nil
	}
	cfg, err := modern.ParseConfig("oracle://fixture:fixture@fixture:1521/service")
	if err != nil {
		return nil, err
	}
	cfg.Dialer = transactionDialer{}
	native, err := modern.NewConnection("", cfg)
	if err != nil {
		return nil, err
	}
	if err := native.OpenWithContext(f.t.Context()); !errors.Is(err, io.EOF) {
		f.t.Fatalf("unexpected synthetic admission: %v", err)
	}
	native.State = modern.Opened
	c := &modernDriverConnection{Connection: native, health: h}
	if !c.IsValid() {
		f.t.Fatal("fixture starts invalid")
	}
	return c, nil
}
func TestCanceledNativeTransactionCannotReturnConnectionToPool(t *testing.T) {
	for _, mode := range []Mode{V2, V3} {
		t.Run(mode.String(), func(t *testing.T) {
			fixture := &transactionPool{t: t, mode: mode}
			pool := sql.OpenDB(fixture)
			pool.SetMaxOpenConns(1)
			defer pool.Close()
			ctx, cancel := context.WithCancel(t.Context())
			tx, err := pool.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			cancel()
			// Conn waits for database/sql's asynchronous canceled-lifetime rollback.
			leaseCtx, stop := context.WithTimeout(t.Context(), 2*time.Second)
			defer stop()
			lease, err := pool.Conn(leaseCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			if fixture.connects.Load() != 2 || fixture.discards.Load() != 1 {
				t.Fatalf("connects=%d discards=%d", fixture.connects.Load(), fixture.discards.Load())
			}
			if err := tx.Rollback(); !errors.Is(err, sql.ErrTxDone) {
				t.Fatalf("transaction not finalized: %v", err)
			}
		})
	}
}

type failedTransaction struct{ cause error }

func (t failedTransaction) Commit() error   { return t.cause }
func (t failedTransaction) Rollback() error { return t.cause }
func TestFailedTransactionCompletionPreservesCauseAndDiscardsOnce(t *testing.T) {
	for _, commit := range []bool{false, true} {
		calls := 0
		cause := errors.New("synthetic transaction completion")
		h := &connectionHealth{discard: func() error { calls++; return nil }}
		tx := &ownedTransaction{Tx: failedTransaction{cause}, health: h}
		var err error
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if !errors.Is(err, cause) || h.valid() || calls != 1 {
			t.Fatalf("cause=%v valid=%v discards=%d", err, h.valid(), calls)
		}
		if err := h.close(func() error { t.Fatal("normal logoff after terminal transaction failure"); return nil }); err != nil {
			t.Fatal(err)
		}
	}
}
