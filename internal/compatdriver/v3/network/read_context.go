package network

import (
	"context"
	"net"
	"sync"
)

// readInterrupt owns only a captured physical transport. Its callback never
// takes the protocol mutex, which can be held by a blocked network write.
type readInterrupt struct {
	conn  net.Conn
	done  chan struct{}
	fired bool // protected by completion of done
	err   error
}

// StartReadContext is the sole cancellation authority for a lazy LOB read.
// Cancellation retires transport rather than sending an unjoined native break.
// The returned join must run before clearing state or disposing the connection.
func (session *Session) StartReadContext(ctx context.Context) func() error {
	session.mu.Lock()
	connection := session.conn
	session.mu.Unlock()
	scope := &readInterrupt{conn: connection, done: make(chan struct{})}
	session.readInterrupt = scope // one leased read; cleared only after its join
	stop := context.AfterFunc(ctx, func() {
		defer close(scope.done)
		scope.fired = true
		if scope.conn != nil {
			scope.err = scope.conn.Close()
		}
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
		return scope.err
	}
}
