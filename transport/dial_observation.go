package transport

import (
	"context"
	"net"
	"time"

	endpoint "github.com/marcuoli/go-hostdiscovery/pkg/hostdiscovery/tcp"
)

// DialContext calls the supplied dialer once and returns the original socket
// and error. A nil dialer uses net.Dialer defaults. The caller owns the socket.
// TCP uses go-hostdiscovery's result. Other networks report the attempted and
// actual addresses and elapsed time with an empty TCP status.
func DialContext(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, endpoint.Result, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return endpoint.DialContext(ctx, dialer, network, address)
	}
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	started := time.Now()
	connection, err := dialer.DialContext(ctx, network, address)
	result := endpoint.Result{Network: network, Address: address, Elapsed: time.Since(started)}
	if connection != nil {
		result.LocalAddress = connection.LocalAddr().String()
		result.RemoteAddress = connection.RemoteAddr().String()
	}
	return connection, result, err
}
