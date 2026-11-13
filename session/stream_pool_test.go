package session_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

func TestStreamNativeScanAndCompleteBoundary(t *testing.T) {
	c := &connection{values: []driver.Value{1.25, nil}, disposed: make(chan struct{})}
	s := open(t, c, options(c))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cursor.Complete() {
		t.Fatal("unread cursor was complete")
	}
	if !cursor.Next() {
		t.Fatal(cursor.Err())
	}
	var number float64
	if err := cursor.Scan(&number); err != nil || number != 1.25 {
		t.Fatalf("native numeric Scan: %v %v", number, err)
	}
	if !cursor.Next() {
		t.Fatal(cursor.Err())
	}
	var nullable any
	if err := cursor.Scan(&nullable); err != nil || nullable != nil {
		t.Fatalf("NULL changed: %v %v", nullable, err)
	}
	if cursor.Next() || cursor.Err() != nil || !cursor.Complete() || !c.cursorClosed.Load() {
		t.Fatalf("EOF did not complete: %v", cursor.Err())
	}
	if err := cursor.Close(); err != nil {
		t.Fatal("Close after EOF:", err)
	}
	if _, err := s.Query(context.Background(), statement()); err == nil {
		t.Fatal("buffered Query stopped rejecting floats")
	}
}

func TestStreamLimitsAndTerminalFailures(t *testing.T) {
	fetchErr, closeErr := errors.New("fetch marker"), errors.New("close marker")
	for _, tc := range []struct {
		name               string
		values             []driver.Value
		fetch, close, want error
		complete           bool
	}{
		{"exact row cap", []driver.Value{"one", "two"}, nil, nil, nil, true},
		{"row beyond cap", []driver.Value{"one", "two", "three"}, nil, nil, session.ErrCapacity, false},
		{"joined cursor failures", []driver.Value{"prefix"}, fetchErr, closeErr, fetchErr, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &connection{values: tc.values, rowErr: tc.fetch, closeRowsErr: tc.close, disposed: make(chan struct{})}
			s := open(t, c, options(c))
			cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{MaxRows: 2})
			if err != nil {
				t.Fatal(err)
			}
			for cursor.Next() {
				var value string
				if err := cursor.Scan(&value); err != nil {
					t.Fatal(err)
				}
			}
			if cursor.Complete() != tc.complete || (tc.want == nil && cursor.Err() != nil) || (tc.want != nil && !errors.Is(cursor.Err(), tc.want)) {
				t.Fatalf("complete=%v err=%v", cursor.Complete(), cursor.Err())
			}
			if tc.close != nil && !errors.Is(cursor.Err(), tc.close) {
				t.Fatal("close cause lost:", cursor.Err())
			}
		})
	}
}

type observedConnector struct {
	c        *connection
	contexts chan context.Context
	closed   chan struct{}
}

func (c observedConnector) Connect(context.Context) (driver.Conn, error) {
	return &observedConn{connection: c.c, contexts: c.contexts, closed: c.closed}, nil
}
func (c observedConnector) Driver() driver.Driver { return fakeDriver{} }

type observedConn struct {
	*connection
	contexts chan context.Context
	closed   chan struct{}
}

func (c *observedConn) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	c.contexts <- ctx
	r, err := c.connection.QueryContext(ctx, text, args)
	if err != nil {
		return nil, err
	}
	return &observedRows{Rows: r, closed: c.closed}, nil
}

type observedRows struct {
	driver.Rows
	closed chan struct{}
	once   sync.Once
}

func (r *observedRows) Close() error {
	err := r.Rows.Close()
	r.once.Do(func() { close(r.closed) })
	return err
}

func TestStreamIdleCancellationAndDeadlineOptOut(t *testing.T) {
	for _, optOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured deadline", true: "parent only"}[optOut], func(t *testing.T) {
			c := &connection{values: []driver.Value{"unread"}, disposed: make(chan struct{})}
			contexts, closed := make(chan context.Context, 1), make(chan struct{})
			opts := options(c)
			opts.QueryTimeout = time.Second
			s, err := session.Open(context.Background(), observedConnector{c, contexts, closed}, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cursor, err := s.Stream(ctx, statement(), session.StreamOptions{NoQueryTimeout: optOut})
			if err != nil {
				t.Fatal(err)
			}
			queryCtx := <-contexts
			if _, bounded := queryCtx.Deadline(); bounded == optOut {
				t.Fatal("wrong query deadline policy")
			}
			cancel()
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("idle cancellation did not close cursor")
			}
			if err := cursor.Close(); !errors.Is(err, context.Canceled) || cursor.Complete() {
				t.Fatalf("cancelled stream: %v", err)
			}
			if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
				t.Fatal("cancelled session reused:", err)
			}
		})
	}
}

func TestPoolStreamRetainsLeaseAndWakesQueuedQuery(t *testing.T) {
	c := &connection{values: []driver.Value{"one"}, disposed: make(chan struct{})}
	p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 1, QueryTimeout: 25 * time.Millisecond, Open: func(ctx context.Context) (*session.Session, error) {
		return session.Open(ctx, connector{c}, options(c))
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	cursor, err := p.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Query(context.Background(), statement())
	if !errors.Is(err, context.DeadlineExceeded) || c.calls.Load() != 1 {
		t.Fatalf("queued query bypassed lease/budget: %v calls=%d", err, c.calls.Load())
	}
	if err := cursor.Close(); !errors.Is(err, context.Canceled) {
		t.Fatal("early close:", err)
	}
	if cursor.Complete() {
		t.Fatal("early close marked complete")
	}
	if _, err := p.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("exhausted pool did not fail:", err)
	}
}

func TestPoolCloseJoinsAnIdleStream(t *testing.T) {
	c := &connection{values: []driver.Value{"idle"}, disposed: make(chan struct{})}
	p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 1, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
		return session.Open(ctx, connector{c}, options(c))
	}})
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := p.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool Close deadlocked with idle stream")
	}
	if !c.cursorClosed.Load() || cursor.Complete() || !errors.Is(cursor.Err(), context.Canceled) {
		t.Fatalf("stream not joined: %v", cursor.Err())
	}
}

func TestPoolFactoryFailureDisposesOpenedSessions(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	calls := 0
	_, err := session.NewPool(context.Background(), session.PoolOptions{Size: 2, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("private-password ORA-01031")
		}
		return session.Open(ctx, connector{c}, options(c))
	}})
	if err == nil {
		t.Fatal("factory error lost")
	}
	select {
	case <-c.disposed:
	default:
		t.Fatal("opened session leaked")
	}
	if err.Error() != "pool-open failed (ORA-01031)" {
		t.Fatal("unsafe factory diagnostic:", err)
	}
}

type blockingCursorConnector struct {
	c         *connection
	entered   chan struct{}
	closeOnly bool
}

func (c blockingCursorConnector) Connect(context.Context) (driver.Conn, error) {
	return &blockingCursorConn{connection: c.c, entered: c.entered, closeOnly: c.closeOnly}, nil
}
func (c blockingCursorConnector) Driver() driver.Driver { return fakeDriver{} }

type blockingCursorConn struct {
	*connection
	entered   chan struct{}
	closeOnly bool
}

func (c *blockingCursorConn) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	r, err := c.connection.QueryContext(ctx, text, args)
	if err != nil {
		return nil, err
	}
	return &blockingCursorRows{Rows: r, entered: c.entered, release: c.disposed, closeOnly: c.closeOnly}, nil
}

type blockingCursorRows struct {
	driver.Rows
	entered   chan struct{}
	release   <-chan struct{}
	closeOnly bool
	once      sync.Once
}

func (r *blockingCursorRows) Next(values []driver.Value) error {
	if !r.closeOnly {
		r.once.Do(func() { close(r.entered) })
		<-r.release
	}
	return r.Rows.Next(values)
}
func (r *blockingCursorRows) Close() error {
	if r.closeOnly {
		r.once.Do(func() { close(r.entered) })
		<-r.release
	}
	return r.Rows.Close()
}

func TestStreamCloseInterruptsBlockedFetch(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	entered := make(chan struct{})
	opts := options(c)
	opts.CleanupTimeout = 20 * time.Millisecond
	s, err := session.Open(context.Background(), blockingCursorConnector{c, entered, false}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.dispose(); s.Close(context.Background()) })
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	next := make(chan bool, 1)
	go func() { next <- cursor.Next() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- cursor.Close() }()
	select {
	case err := <-closed:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close could not interrupt fetch")
	}
	if <-next || !c.cursorClosed.Load() || cursor.Complete() {
		t.Fatal("fetch/close ownership not joined")
	}
}

func TestStreamCleanupRemainsBoundedWithoutQueryDeadline(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	entered := make(chan struct{})
	opts := options(c)
	opts.CleanupTimeout = 20 * time.Millisecond
	s, err := session.Open(context.Background(), blockingCursorConnector{c, entered, true}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.dispose(); s.Close(context.Background()) })
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{NoQueryTimeout: true})
	if err != nil {
		t.Fatal(err)
	}
	next := make(chan bool, 1)
	go func() { next <- cursor.Next() }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cursor close did not start")
	}
	select {
	case got := <-next:
		if got {
			t.Fatal("empty cursor returned a row")
		}
	case <-time.After(time.Second):
		t.Fatal("unbounded cursor cleanup")
	}
	if cursor.Complete() || !errors.Is(cursor.Err(), context.DeadlineExceeded) {
		t.Fatalf("cleanup failure lost: %v", cursor.Err())
	}
}

func TestStreamScanFailureIsSafeAndIncomplete(t *testing.T) {
	c := &connection{values: []driver.Value{"private-password"}, disposed: make(chan struct{})}
	s := open(t, c, options(c))
	cursor, err := s.Stream(context.Background(), statement(), session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cursor.Next() {
		t.Fatal(cursor.Err())
	}
	var number int
	err = cursor.Scan(&number)
	if err == nil || strings.Contains(err.Error(), "private-password") || cursor.Complete() || !c.cursorClosed.Load() {
		t.Fatalf("unsafe/incomplete conversion: %v", err)
	}
	if _, err := s.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("failed stream was reused")
	}
}

func TestPoolLeasesBoundConcurrentQueries(t *testing.T) {
	connections := []*reviewConn{{entered: make(chan struct{}), release: make(chan struct{})}, {entered: make(chan struct{}), release: make(chan struct{})}}
	i := 0
	p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 2, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
		c := connections[i]
		i++
		return session.Open(ctx, reviewConnector{c}, session.Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: 20 * time.Millisecond, MaxRows: 2, MaxBytes: 128, Dispose: c.dispose})
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range connections {
			c.dispose()
		}
		p.Close(context.Background())
	})
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := p.Query(context.Background(), reviewStatement()); done <- err }()
	}
	for _, c := range connections {
		select {
		case <-c.entered:
		case <-time.After(time.Second):
			t.Fatal("two sessions did not execute concurrently")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.Query(ctx, reviewStatement()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued request exceeded the pool:", err)
	}
	for _, c := range connections {
		c.dispose()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal("queued cancellation disturbed active query:", err)
		}
	}
}

func TestPoolNeverReplaysOrRecoversImplicitly(t *testing.T) {
	c := &connection{queryErr: driver.ErrBadConn, disposed: make(chan struct{})}
	opens := 0
	p, err := session.NewPool(context.Background(), session.PoolOptions{Size: 1, QueryTimeout: time.Second, Open: func(ctx context.Context) (*session.Session, error) {
		opens++
		return session.Open(ctx, connector{c}, options(c))
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if _, err := p.Query(context.Background(), statement()); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	if _, err := p.Query(context.Background(), statement()); !errors.Is(err, session.ErrUnavailable) {
		t.Fatal("exhausted pool did not fail:", err)
	}
	if opens != 1 || c.calls.Load() != 1 {
		t.Fatal("pool replayed SQL or silently opened a replacement")
	}
}

func TestStreamRejectsNegativeLimitBeforeExecution(t *testing.T) {
	c := &connection{disposed: make(chan struct{})}
	s := open(t, c, options(c))
	if _, err := s.Stream(context.Background(), statement(), session.StreamOptions{MaxRows: -1}); err == nil || c.calls.Load() != 0 {
		t.Fatal("negative stream bound executed SQL")
	}
	if _, err := s.Query(context.Background(), statement()); err != nil {
		t.Fatal("invalid options retired an unused session:", err)
	}
}
