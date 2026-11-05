// Package transport tracks the physical sockets owned by one injected connector.
package transport

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	endpoint "github.com/marcuoli/go-hostdiscovery/pkg/hostdiscovery/tcp"
)

// ErrClosed identifies a permanently discarded transport group.
var ErrClosed = errors.New("session transport is closed")

// DialFunc permits deterministic transport tests without network discovery.
type DialFunc func(context.Context, string, string) (net.Conn, error)

// Dialer returns actual sockets, preserving driver TLS/OOB behavior. It belongs
// to one physical session and its redirects, not independent pool connections.
type Dialer struct {
	mu          sync.Mutex
	dialer      net.Dialer
	injected    DialFunc
	connections []net.Conn
	closed      bool
	closeOnce   sync.Once
	closeErr    error
}

// New constructs a direct endpoint dialer with an explicit positive timeout.
func New(timeout time.Duration) (*Dialer, error) {
	if timeout <= 0 {
		return nil, errors.New("connection timeout must be positive")
	}
	return &Dialer{dialer: net.Dialer{Timeout: timeout}}, nil
}

// WithDialer constructs the same lifecycle with a caller-supplied direct dialer.
func WithDialer(timeout time.Duration, dial DialFunc) (*Dialer, error) {
	transport, err := New(timeout)
	if err != nil {
		return nil, err
	}
	transport.injected = dial
	return transport, nil
}

// DialContext contacts only the requested endpoint and retains the actual socket.
// A connection completing concurrently with Close is closed before return.
func (transport *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	transport.mu.Lock()
	closed := transport.closed
	transport.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	var connection net.Conn
	var err error
	if transport.injected != nil {
		connection, err = transport.injected(ctx, network, address)
	} else if network == "tcp" || network == "tcp4" || network == "tcp6" {
		connection, _, err = endpoint.DialContext(ctx, &transport.dialer, network, address)
	} else {
		connection, err = transport.dialer.DialContext(ctx, network, address)
	}
	if err != nil {
		return nil, err
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if transport.closed {
		_ = connection.Close()
		return nil, ErrClosed
	}
	transport.connections = append(transport.connections, connection)
	return connection, nil
}

// Close permanently disposes all tracked redirects and joins their closes.
// Already-closed sockets do not turn successful disposal into a new failure.
func (transport *Dialer) Close() error {
	transport.closeOnce.Do(func() {
		transport.mu.Lock()
		transport.closed = true
		connections := transport.connections
		transport.connections = nil
		transport.mu.Unlock()
		var failures []error
		for _, connection := range connections {
			if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				failures = append(failures, err)
			}
		}
		transport.closeErr = errors.Join(failures...)
	})
	return transport.closeErr
}
