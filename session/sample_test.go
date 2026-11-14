package session_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	sessioncancel "github.com/marcuoli/go-oracle-session/cancel"
	"github.com/marcuoli/go-oracle-session/session"
)

type sampleConnection struct {
	*connection
	nexts atomic.Int32
}

func (c *sampleConnection) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.connection.QueryContext(ctx, text, args)
	if err != nil {
		return nil, err
	}
	return &sampleRows{Rows: rows, nexts: &c.nexts}, nil
}

type sampleRows struct {
	driver.Rows
	nexts *atomic.Int32
}

func (rows *sampleRows) Next(values []driver.Value) error {
	rows.nexts.Add(1)
	return rows.Rows.Next(values)
}

type sampleConnector struct{ c *sampleConnection }

func (c sampleConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (sampleConnector) Driver() driver.Driver                          { return fakeDriver{} }

func openSample(t *testing.T, c *sampleConnection, opts session.Options) *session.Session {
	t.Helper()
	client, err := session.Open(context.Background(), sampleConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return client
}

// qualifiedSample is synthetic contract evidence, not native driver qualification.
func qualifiedSample(ctx context.Context, native any) (session.SampleHealth, error) {
	if err := ctx.Err(); err != nil {
		return session.SampleHealth{}, err
	}
	if _, ok := native.(*sampleConnection); !ok {
		return session.SampleHealth{}, errors.New("unexpected native connection")
	}
	return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nil
}

func TestFinalizeSamplePreservesSessionWithoutDraining(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"first", "second", "third"}, disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cursor.Next() {
		t.Fatal(cursor.Err())
	}
	var retained string
	if err := cursor.Scan(&retained); err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	finalizer := func(ctx context.Context, native any) (session.SampleHealth, error) {
		probes.Add(1)
		if !c.cursorClosed.Load() || c.nexts.Load() != 1 {
			t.Error("probe preceded cursor close or drained unread rows")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("finalizer has no cleanup budget")
		}
		return qualifiedSample(ctx, native)
	}
	outcome, err := cursor.FinalizeSample(finalizer)
	if err != nil || outcome.EOFObserved || !outcome.StoppedEarly || sessioncancel.Classify(outcome.Evidence) != sessioncancel.Reusable {
		t.Fatalf("partial finalization: %+v %v", outcome, err)
	}
	if cursor.Complete() || cursor.Err() != nil || retained != "first" || c.nexts.Load() != 1 {
		t.Fatal("partial retention became EOF or changed its values")
	}
	again, err := cursor.FinalizeSample(finalizer)
	if err != nil || again != outcome || probes.Load() != 1 {
		t.Fatalf("finalization replayed: %+v %v probes=%d", again, err, probes.Load())
	}
	if err := cursor.Close(); err != nil {
		t.Fatal("Close after joined sample:", err)
	}
	result, err := s.Query(context.Background(), statement())
	if err != nil || len(result.Rows) != 3 || c.calls.Load() != 2 {
		t.Fatalf("same session did not continue exactly once: %+v %v calls=%d", result, err, c.calls.Load())
	}
}

func TestFinalizeSampleRequiresAffirmativeHealth(t *testing.T) {
	probeErr := errors.New("probe marker")
	for _, tc := range []struct {
		name      string
		finalizer session.SampleFinalizer
		want      error
	}{
		{"missing callback", nil, session.ErrUnsupported},
		{"missing protocol proof", func(context.Context, any) (session.SampleHealth, error) {
			return session.SampleHealth{TransactionUsable: true}, nil
		}, session.ErrUnavailable},
		{"missing transaction proof", func(context.Context, any) (session.SampleHealth, error) {
			return session.SampleHealth{ProtocolSettled: true}, nil
		}, session.ErrUnavailable},
		{"probe failure", func(context.Context, any) (session.SampleHealth, error) {
			return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, probeErr
		}, probeErr},
		{"bad connection is not replayed", func(context.Context, any) (session.SampleHealth, error) {
			return session.SampleHealth{}, driver.ErrBadConn
		}, driver.ErrBadConn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &sampleConnection{connection: &connection{values: []driver.Value{"prefix", "unread"}, disposed: make(chan struct{})}}
			s := openSample(t, c, options(c.connection))
			cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := cursor.FinalizeSample(tc.finalizer)
			if !errors.Is(err, tc.want) || outcome.EOFObserved || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable || cursor.Complete() {
				t.Fatalf("missing proof admitted reuse: %+v %v", outcome, err)
			}
			if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) || c.calls.Load() != 1 {
				t.Fatalf("failed sample replayed/reused: %v calls=%d", err, c.calls.Load())
			}
		})
	}
}

func TestFinalizeSampleRetainsIndependentTerminalErrors(t *testing.T) {
	fetchErr, closeErr := errors.New("fetch marker"), errors.New("close marker")
	c := &sampleConnection{connection: &connection{values: []driver.Value{"prefix"}, rowErr: fetchErr, closeRowsErr: closeErr, disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for cursor.Next() {
		var value string
		if err := cursor.Scan(&value); err != nil {
			t.Fatal(err)
		}
	}
	var probes atomic.Int32
	outcome, err := cursor.FinalizeSample(func(ctx context.Context, native any) (session.SampleHealth, error) {
		probes.Add(1)
		return qualifiedSample(ctx, native)
	})
	if !errors.Is(err, fetchErr) || !errors.Is(err, closeErr) || outcome.EOFObserved || probes.Load() != 0 || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable {
		t.Fatalf("terminal evidence changed: %+v %v probes=%d", outcome, err, probes.Load())
	}
}

func TestFinalizeSampleCloseFailureCannotBeOverridden(t *testing.T) {
	closeErr := errors.New("close marker")
	c := &sampleConnection{connection: &connection{values: []driver.Value{"unread"}, closeRowsErr: closeErr, disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	outcome, err := cursor.FinalizeSample(func(ctx context.Context, native any) (session.SampleHealth, error) {
		probes.Add(1)
		return qualifiedSample(ctx, native)
	})
	if !errors.Is(err, closeErr) || probes.Load() != 0 || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable {
		t.Fatalf("close failure hidden: %+v %v probes=%d", outcome, err, probes.Load())
	}
}

func TestFinalizeSampleReleasesQualifiedPoolCapacity(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"one", "two"}, disposed: make(chan struct{})}}
	var opens atomic.Int32
	pool, err := session.NewPool(context.Background(), session.PoolOptions{
		Size: 1, QueryTimeout: time.Second,
		Open: func(ctx context.Context) (*session.Session, error) {
			opens.Add(1)
			return session.Open(ctx, sampleConnector{c}, options(c.connection))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pool.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	cursor, err := pool.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cursor.FinalizeSample(qualifiedSample); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Query(context.Background(), statement()); err != nil || opens.Load() != 1 {
		t.Fatalf("qualified pool slot lost or reconnected: %v opens=%d", err, opens.Load())
	}
}

func TestFinalizeSampleBoundedWithoutQueryDeadline(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"unread"}, disposed: make(chan struct{})}}
	opts := options(c.connection)
	opts.CleanupTimeout = 20 * time.Millisecond
	s := openSample(t, c, opts)
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	probeErr := errors.New("unblocked finalizer")
	outcome, err := cursor.FinalizeSample(func(ctx context.Context, native any) (session.SampleHealth, error) {
		<-c.disposed
		return session.SampleHealth{}, errors.Join(ctx.Err(), probeErr)
	})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, probeErr) || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable || cursor.Complete() {
		t.Fatalf("unbounded/hidden probe failure: %+v %v", outcome, err)
	}
	if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("timed-out finalizer session reused:", err)
	}
}

func TestFinalizeSamplePanicJoinsOwnedState(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"unread"}, disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	marker := &struct{ name string }{"callback panic"}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		cursor.FinalizeSample(func(context.Context, any) (session.SampleHealth, error) { panic(marker) })
	}()
	if recovered != marker {
		t.Fatalf("callback panic changed: %v", recovered)
	}
	if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("panic leaked or reused the lease:", err)
	}
	if err := cursor.Err(); err == nil {
		t.Fatal("panic cleanup left no failure evidence")
	}
}

func TestFinalizeSampleAfterEOFDoesNotProbe(t *testing.T) {
	c := &sampleConnection{connection: &connection{disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Next() || cursor.Err() != nil || !cursor.Complete() {
		t.Fatal("empty EOF failed:", cursor.Err())
	}
	outcome, err := cursor.FinalizeSample(nil)
	if err != nil || !outcome.EOFObserved || outcome.StoppedEarly || sessioncancel.Classify(outcome.Evidence) != sessioncancel.Reusable {
		t.Fatalf("known EOF changed: %+v %v", outcome, err)
	}
}

func TestFinalizeSampleInterruptionCannotBeOverridden(t *testing.T) {
	for _, interrupt := range []string{"parent", "cursor close", "session close"} {
		t.Run(interrupt, func(t *testing.T) {
			c := &sampleConnection{connection: &connection{values: []driver.Value{"unread"}, disposed: make(chan struct{})}}
			opts := options(c.connection)
			opts.QueryTimeout = 5 * time.Second
			s := openSample(t, c, opts)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cursor, err := s.Stream(ctx, statement(), session.StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			type result struct {
				outcome session.SampleFinalization
				err     error
			}
			finalized := make(chan result, 1)
			go func() {
				outcome, err := cursor.FinalizeSample(func(ctx context.Context, _ any) (session.SampleHealth, error) {
					close(entered)
					<-ctx.Done()
					// Even affirmative flags cannot override cancellation.
					return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nil
				})
				finalized <- result{outcome, err}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("finalizer did not start")
			}
			closed := make(chan error, 1)
			switch interrupt {
			case "parent":
				cancel()
				closed <- nil
			case "cursor close":
				go func() { closed <- cursor.Close() }()
			case "session close":
				go func() { closed <- s.Close(context.Background()) }()
			}
			select {
			case got := <-finalized:
				if !errors.Is(got.err, context.Canceled) || sessioncancel.Classify(got.outcome.Evidence) == sessioncancel.Reusable || cursor.Complete() {
					t.Fatalf("interruption lost: %+v %v", got.outcome, got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("interruption did not join finalizer")
			}
			select {
			case err := <-closed:
				if interrupt == "cursor close" && !errors.Is(err, context.Canceled) {
					t.Fatal("concurrent Close lost cancellation:", err)
				}
				if interrupt == "session close" && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("close did not join")
			}
		})
	}
}

func TestFinalizeSampleRejectsTransactionWithoutDriverCallback(t *testing.T) {
	c := executionFixture()
	s := executionOpen(t, c)
	tx, err := s.BeginTx(context.Background(), session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback(context.Background()) })
	cursor, err := tx.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var probes atomic.Int32
	outcome, err := cursor.FinalizeSample(func(context.Context, any) (session.SampleHealth, error) {
		probes.Add(1)
		return session.SampleHealth{}, driver.ErrBadConn
	})
	if !errors.Is(err, session.ErrUnsupported) || probes.Load() != 0 || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable {
		t.Fatalf("transaction borrowed Raw connection: %+v %v probes=%d", outcome, err, probes.Load())
	}
	if err := tx.Rollback(context.Background()); err != nil || c.rollbacks.Load() != 1 {
		t.Fatal("transaction cleanup failed:", err, c.rollbacks.Load())
	}
	if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("unsupported transaction sample reused:", err)
	}
}

func TestFinalizeSamplePreservesPreparedOwnership(t *testing.T) {
	c := &preparedConn{execution: executionFixture()}
	opts := options(c.execution.connection)
	opts.PreparedContext = true
	s, err := session.Open(context.Background(), preparedConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := cursor.FinalizeSample(func(_ context.Context, native any) (session.SampleHealth, error) {
		if native != c || c.closed.Load() != 1 {
			t.Error("original driver or statement close ownership changed")
		}
		return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nil
	})
	if err != nil || outcome.EOFObserved || sessioncancel.Classify(outcome.Evidence) != sessioncancel.Reusable {
		t.Fatalf("prepared finalization: %+v %v", outcome, err)
	}
	if _, err := s.Query(context.Background(), statement()); err != nil || c.prepared.Load() != 2 || c.closed.Load() != 2 || c.legacy.Load() != 0 {
		t.Fatal("prepared session failed to continue without fallback:", err)
	}
}

func TestFinalizeSampleCannotEraseRowCapacityFailure(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"retained", "extra"}, disposed: make(chan struct{})}}
	s := openSample(t, c, options(c.connection))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !cursor.Next() || cursor.Next() {
		t.Fatal("fixture failed to reach capacity:", cursor.Err())
	}
	outcome, err := cursor.FinalizeSample(qualifiedSample)
	if !errors.Is(err, session.ErrCapacity) || outcome.StoppedEarly || outcome.EOFObserved || sessioncancel.Classify(outcome.Evidence) == sessioncancel.Reusable {
		t.Fatalf("capacity failure changed: %+v %v", outcome, err)
	}
}

func TestFinalizeSampleRetainsFailedDisposalWithoutReuseProof(t *testing.T) {
	c := &sampleConnection{connection: &connection{values: []driver.Value{"unread"}, disposed: make(chan struct{})}}
	disposeErr := errors.New("disposal marker")
	probeErr := errors.New("private-credential ORA-01013")
	opts := options(c.connection)
	opts.Dispose = func() error { c.dispose(); return disposeErr }
	s, err := session.Open(context.Background(), sampleConnector{c}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); !errors.Is(err, disposeErr) {
			t.Fatal("disposal failure disappeared:", err)
		}
	})
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := cursor.FinalizeSample(func(context.Context, any) (session.SampleHealth, error) {
		return session.SampleHealth{}, probeErr
	})
	if !errors.Is(err, probeErr) || !errors.Is(err, disposeErr) || outcome.Evidence.TransportDiscarded || sessioncancel.Classify(outcome.Evidence) != sessioncancel.Unknown {
		t.Fatalf("failed disposal became affirmative evidence: %+v %v", outcome, err)
	}
	if err.Error() != "stream failed (ORA-01013)" {
		t.Fatal("unsafe finalizer error formatting:", err)
	}
}
