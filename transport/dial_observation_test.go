package transport_test

import (
	"context"
	"errors"
	"net"
	"runtime"
	"syscall"
	"testing"
	"time"

	endpoint "github.com/marcuoli/go-hostdiscovery/pkg/hostdiscovery/tcp"
	"github.com/marcuoli/go-oracle-session/transport"
)

func TestDialContextObservesOriginalTCPConnection(t *testing.T) {
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type contextKey struct{}
	ctx = context.WithValue(ctx, contextKey{}, "original")
	controls := 0
	address := listener.Addr().String()
	dialer := &net.Dialer{
		ControlContext: func(ctx context.Context, _, target string, _ syscall.RawConn) error {
			controls++
			if ctx.Value(contextKey{}) != "original" || target != address {
				return errors.New("dial did not receive the caller's context or address")
			}
			return nil
		},
	}
	connection, got, err := transport.DialContext(ctx, dialer, "tcp4", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, ok := connection.(*net.TCPConn); !ok {
		t.Fatalf("connection type = %T", connection)
	}
	if controls != 1 || got.Status != endpoint.Connected || got.Network != "tcp4" || got.Address != address ||
		got.LocalAddress != connection.LocalAddr().String() || got.RemoteAddress != address || got.Elapsed <= 0 {
		t.Fatalf("controls = %d, observation = %+v", controls, got)
	}
	if err := connection.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("still owned by the caller")); err != nil {
		t.Fatal(err)
	}
}

func TestDialContextPreservesDialError(t *testing.T) {
	rejected := errors.New("injected control refusal")
	controls := 0
	dialer := &net.Dialer{
		ControlContext: func(context.Context, string, string, syscall.RawConn) error {
			controls++
			return rejected
		},
	}
	const address = "127.0.0.1:1521"
	connection, got, err := transport.DialContext(t.Context(), dialer, "tcp4", address)
	var dialError *net.OpError
	if connection != nil || controls != 1 || !errors.As(err, &dialError) || !errors.Is(err, rejected) ||
		got.Status != endpoint.Failed || got.Address != address || got.LocalAddress != "" || got.RemoteAddress != "" {
		t.Fatalf("connection = %v, controls = %d, observation = %+v, error = %v", connection, controls, got, err)
	}
}

func TestDialContextUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-domain socket fixture runs on Unix")
	}
	// A relative name fits sockaddr_un even when runner scratch is deeply nested.
	t.Chdir(t.TempDir())
	const address = "s"
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	connection, got, err := transport.DialContext(ctx, nil, "unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, ok := connection.(*net.UnixConn); !ok {
		t.Fatalf("connection type = %T", connection)
	}
	if got.Status != "" || got.Network != "unix" || got.Address != address ||
		got.LocalAddress != connection.LocalAddr().String() || got.RemoteAddress != address || got.Elapsed <= 0 {
		t.Fatalf("Unix dial observation = %+v", got)
	}
}
