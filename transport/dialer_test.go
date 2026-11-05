package transport_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/transport"
)

func TestCloseOwnsAllSocketsAndRejectsLaterDials(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	dialer, err := transport.WithDialer(time.Second, func(context.Context, string, string) (net.Conn, error) { return left, nil })
	if err != nil {
		t.Fatal(err)
	}
	actual, err := dialer.DialContext(context.Background(), "tcp", "example.invalid:1521")
	if err != nil || actual != left {
		t.Fatal("socket identity changed")
	}
	if err := dialer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := right.Write([]byte("x")); err == nil {
		t.Fatal("physical socket remained open")
	}
	if _, err := dialer.DialContext(context.Background(), "tcp", "example.invalid:1521"); !errors.Is(err, transport.ErrClosed) {
		t.Fatal("discarded transport accepted a connection")
	}
}

func TestDialCompletingAfterDiscardIsNotReturned(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	left, right := net.Pipe()
	defer right.Close()
	dialer, err := transport.WithDialer(time.Second, func(context.Context, string, string) (net.Conn, error) { close(entered); <-release; return left, nil })
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := dialer.DialContext(context.Background(), "tcp", "example.invalid:1521")
		result <- err
	}()
	<-entered
	if err := dialer.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, transport.ErrClosed) {
		t.Fatal("racing connection escaped disposal")
	}
	if _, err := right.Write([]byte("x")); err == nil {
		t.Fatal("racing socket remained open")
	}
}
