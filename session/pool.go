package session

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/marcuoli/go-sqltemplate/reference"
)

// OpenFunc opens and admits a fresh independent session. On success ownership
// transfers exclusively to the pool; callers must not retain/use the session.
// Its connector and disposal hook must not share another session's physical I/O.
type OpenFunc func(context.Context) (*Session, error)

// PoolOptions requires positive Size and QueryTimeout and a non-nil Open factory.
// QueryTimeout includes waiting for a lease; session budgets can expire earlier.
// The factory retains connection admission, initialization and native qualification.
type PoolOptions struct {
	Size         int
	QueryTimeout time.Duration
	Open         OpenFunc
}

// Pool leases a fixed set of independent sessions. It never replays SQL or opens
// replacement connections. Unusable sessions are retired and capacity shrinks;
// once exhausted, operations return ErrUnavailable. Close owns all sessions.
type Pool struct {
	options           PoolOptions
	sessions          []*Session
	ready             chan *Session
	closed, exhausted chan struct{}
	mu                sync.Mutex
	closing           bool
	live              int
	closeOnce         sync.Once
	closeErr          error
}

// NewPool opens and admits Size distinct sessions sequentially. Setup failure
// closes every returned session and joins cleanup causes before returning. Driver
// factories/hooks must obey their existing bounded-I/O contracts.
func NewPool(ctx context.Context, options PoolOptions) (*Pool, error) {
	if options.Size < 1 || options.QueryTimeout <= 0 || options.Open == nil {
		return nil, errors.New("invalid pool size/budget/factory")
	}
	pool := &Pool{options: options, ready: make(chan *Session, options.Size), closed: make(chan struct{}), exhausted: make(chan struct{})}
	seen := make(map[*Session]struct{}, options.Size)
	for range options.Size {
		if err := ctx.Err(); err != nil {
			return nil, safeError("pool-open", reference.Reference{}, errors.Join(err, pool.Close(ctx)))
		}
		client, err := options.Open(ctx)
		if client != nil {
			if _, duplicate := seen[client]; duplicate {
				err = errors.Join(err, errors.New("factory returned duplicate session"))
			} else {
				seen[client] = struct{}{}
				pool.sessions = append(pool.sessions, client)
				pool.live++
			}
		}
		if client == nil && err == nil {
			err = errors.New("factory returned nil session")
		}
		if err == nil && !client.available() {
			err = ErrUnavailable
		}
		if err != nil {
			return nil, safeError("pool-open", reference.Reference{}, errors.Join(err, pool.Close(ctx)))
		}
	}
	for _, client := range pool.sessions {
		pool.ready <- client
	}
	return pool, nil
}

func (pool *Pool) acquire(ctx context.Context) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pool.closed:
		return nil, ErrUnavailable
	case <-pool.exhausted:
		return nil, ErrUnavailable
	case client := <-pool.ready:
		pool.mu.Lock()
		closing := pool.closing
		pool.mu.Unlock()
		if closing {
			return nil, ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, pool.release(client))
		}
		return client, nil
	}
}

func (pool *Pool) release(client *Session) error {
	pool.mu.Lock()
	if pool.closing {
		pool.mu.Unlock()
		return nil
	}
	pool.mu.Unlock()
	available := client.available()
	pool.mu.Lock()
	if pool.closing {
		pool.mu.Unlock()
		return nil
	}
	if available {
		pool.ready <- client
		pool.mu.Unlock()
		return nil
	}
	pool.live--
	if pool.live == 0 {
		close(pool.exhausted)
	}
	pool.mu.Unlock()
	// Shutdown marks ownership before closing sessions, so a stream's return
	// callback never recursively closes the session already closing its stream.
	// Retirement can run on the stream's finalizer itself. Public shutdown
	// joins that worker separately; internal physical close must not self-join.
	return client.close(context.Background(), false)
}

// Query leases one session and returns its complete exact buffered result. The
// pool deadline includes queueing. A failed query is never replayed on another
// session; cleanup failures also invalidate the returned result.
func (pool *Pool) Query(ctx context.Context, statement Statement) (result Result, returnedErr error) {
	operation, cancel := context.WithTimeout(ctx, pool.options.QueryTimeout)
	defer cancel()
	client, err := pool.acquire(operation)
	if err != nil {
		return Result{}, safeError("pool-query", statement.Reference, err)
	}
	defer func() {
		returnedErr = joinCauses(returnedErr, pool.release(client))
		if returnedErr != nil {
			result = Result{}
		}
	}()
	return client.Query(operation, statement)
}

// Stream leases one session until its native cursor finishes. NoQueryTimeout
// opts out of both the pool query/queue deadline and the session query deadline,
// retaining caller cancellation and positive connection/cleanup budgets.
func (pool *Pool) Stream(ctx context.Context, statement Statement, options StreamOptions) (*Stream, error) {
	var operation context.Context
	var cancel context.CancelFunc
	if options.NoQueryTimeout {
		operation, cancel = context.WithCancel(ctx)
	} else {
		operation, cancel = context.WithTimeout(ctx, pool.options.QueryTimeout)
	}
	client, err := pool.acquire(operation)
	if err != nil {
		cancel()
		return nil, safeError("pool-stream", statement.Reference, err)
	}
	return client.startStream(operation, ctx, statement, options, func() error { cancel(); return pool.release(client) })
}

// Close wakes queued callers, cancels active streams and joins all owned session
// cleanup in at most Size concurrent workers. It is idempotent. Cleanup may
// outlive caller cancellation within the sessions' qualified cleanup budgets.
func (pool *Pool) Close(ctx context.Context) error {
	pool.closeOnce.Do(func() {
		pool.mu.Lock()
		pool.closing = true
		close(pool.closed)
		pool.mu.Unlock()
		errs := make([]error, len(pool.sessions))
		var workers sync.WaitGroup
		for i, client := range pool.sessions {
			workers.Go(func() { errs[i] = client.Close(ctx) })
		}
		workers.Wait()
		pool.closeErr = safeError("pool-close", reference.Reference{}, errors.Join(errs...))
	})
	return pool.closeErr
}
