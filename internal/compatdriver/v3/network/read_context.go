package network

import (
	"context"
	"net"
	"sync"
)

// readInterrupt shares one physical close between cancellation and disposal.
// Its close never takes the protocol mutex, which a blocked write may hold.
type readInterrupt struct {
	conn      net.Conn
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	err       error
	fired     bool // only inspected after callback completion
}

func (scope *readInterrupt) close() error {
	scope.closeOnce.Do(func() {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		if scope.conn != nil {
			scope.err = scope.conn.Close()
		}
	})
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.err
}

// StartReadContext is the sole cancellation authority for a lazy LOB read.
// Cancellation retires transport rather than sending an unjoined native break.
// The returned join stops/joins its callback before the read releases ownership.
func (session *Session) StartReadContext(ctx context.Context) func() error {
	session.readMu.Lock()
	session.mu.Lock()
	scope := &readInterrupt{conn: session.conn, done: make(chan struct{})}
	session.readInterrupt = scope
	session.mu.Unlock()
	session.readMu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		defer close(scope.done)
		scope.fired = true
		scope.close()
	})
	var once sync.Once
	return func() error {
		once.Do(func() {
			if stop() {
				close(scope.done)
			} else {
				<-scope.done
			}
		})
		scope.mu.Lock()
		defer scope.mu.Unlock()
		return scope.err
	}
}
