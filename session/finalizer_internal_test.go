package session

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/marcuoli/go-sqltemplate/reference"
)

type finalizerConnector struct {
	columns  []string
	queryErr error
}

func (finalizerConnector) Driver() driver.Driver { return finalizerDriver{} }
func (connector finalizerConnector) Connect(context.Context) (driver.Conn, error) {
	return finalizerConnection{columns: connector.columns, queryErr: connector.queryErr}, nil
}

type finalizerDriver struct{}

func (finalizerDriver) Open(string) (driver.Conn, error) { return nil, errors.New("unexpected open") }

type finalizerConnection struct {
	columns  []string
	queryErr error
}

func (finalizerConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (finalizerConnection) Begin() (driver.Tx, error) { return nil, errors.New("unexpected begin") }
func (finalizerConnection) Close() error              { return nil }
func (connection finalizerConnection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if connection.queryErr != nil {
		return nil, connection.queryErr
	}
	return finalizerRows{columns: connection.columns}, nil
}

type finalizerRows struct{ columns []string }

func (rows finalizerRows) Columns() []string {
	if rows.columns != nil {
		return rows.columns
	}
	return []string{"VALUE"}
}
func (finalizerRows) Close() error              { return nil }
func (finalizerRows) Next([]driver.Value) error { return io.EOF }

func TestSessionShutdownJoinsStreamFinalizerAfterLeaseRelease(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancelled", false: "EOF"}[cancelled], func(t *testing.T) {
			testShutdownFinalizer(t, cancelled)
		})
	}
}

func testShutdownFinalizer(t *testing.T, cancelled bool) {
	s, err := Open(context.Background(), finalizerConnector{}, Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second, MaxRows: 1, MaxBytes: 128, Dispose: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	entered, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ref, _ := reference.NewReference("example", "shutdown", "finalizer", 1)
	cursor, err := s.startStream(ctx, ctx, Statement{Reference: ref, Text: "SELECT value FROM example_table"}, StreamOptions{}, func() error { close(entered); <-resume; close(finished); return nil })
	if err != nil {
		t.Fatal(err)
	}
	nextDone := make(chan struct{})
	if cancelled {
		cancel()
		close(nextDone)
	} else {
		go func() { cursor.Next(); close(nextDone) }()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("finalizer did not reach lease-return callback")
	}
	type outcome struct {
		err    error
		joined bool
	}
	done := make(chan outcome, 1)
	go func() {
		err := s.Close(context.Background())
		select {
		case <-finished:
			done <- outcome{err, true}
		default:
			done <- outcome{err, false}
		}
	}()
	var result outcome
	returned := false
	select {
	case result = <-done:
		returned = true
	case <-time.After(30 * time.Millisecond):
	}
	close(resume)
	if !returned {
		select {
		case result = <-done:
		case <-time.After(time.Second):
			t.Fatal("shutdown did not join finalizer")
		}
	}
	if result.err != nil || !result.joined {
		t.Fatalf("Close returned before owned finalizer: joined=%v err=%v", result.joined, result.err)
	}
	<-nextDone
}

func TestShutdownJoinsFinalizerRegisteredAfterClosingStarts(t *testing.T) {
	s, err := Open(context.Background(), finalizerConnector{}, Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second, MaxRows: 1, MaxBytes: 128, Dispose: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		s.Close(context.Background())
		select {
		case <-joined:
			done <- true
		default:
			done <- false
		}
	}()
	// Model a stream admitted before closing, registering its finalizer after
	// shutdown has set closing but before it can acquire the session lease.
	deadline := time.Now().Add(time.Second)
	for !s.closing.Load() {
		if time.Now().After(deadline) {
			s.release()
			t.Fatal("shutdown did not start")
		}
		runtime.Gosched()
	}
	s.streamFinalizers.Add(1)
	s.release()
	returned := false
	var got bool
	select {
	case got = <-done:
		returned = true
	case <-time.After(30 * time.Millisecond):
	}
	close(joined)
	s.streamFinalizers.Done()
	if !returned {
		select {
		case got = <-done:
		case <-time.After(time.Second):
			t.Fatal("shutdown did not join late finalizer")
		}
	}
	if !got {
		t.Fatal("Close missed a finalizer registered after closing started")
	}
}

func TestStreamStartupFailureJoinsFinalizer(t *testing.T) {
	for _, tc := range []struct {
		name      string
		connector finalizerConnector
		want      error
	}{
		{"query", finalizerConnector{queryErr: driver.ErrBadConn}, driver.ErrBadConn},
		{"projection", finalizerConnector{columns: []string{}}, ErrCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(context.Background(), tc.connector, Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second, MaxRows: 1, MaxBytes: 128, Dispose: func() error { return nil }})
			if err != nil {
				t.Fatal(err)
			}
			ref, _ := reference.NewReference("example", "startup", "fixture", 1)
			finished := make(chan struct{})
			cursor, err := s.startStream(context.Background(), context.Background(), Statement{Reference: ref, Text: "SELECT value FROM example_table"}, StreamOptions{}, func() error { close(finished); return nil })
			if cursor != nil || !errors.Is(err, tc.want) {
				t.Fatalf("startup result=%v error=%v", cursor, err)
			}
			select {
			case <-finished:
			default:
				t.Fatal("startup failure lost lease-return callback")
			}
			done := make(chan error, 1)
			go func() { done <- s.Close(context.Background()) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("startup failure leaked a finalizer")
			}
		})
	}
}
