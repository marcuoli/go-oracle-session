package compat

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
)

type unsupportedProtocolPinger struct {
	fixtureConnection
}

func (unsupportedProtocolPinger) Ping(context.Context) error {
	panic("a generic driver ping may run SQL")
}

func TestPingProtocolRejectsForeignOrUnknownCapability(t *testing.T) {
	for _, connection := range []driver.Conn{
		&unsupportedProtocolPinger{},
		&legacyDriverConnection{Connection: &legacy.Connection{}},
		(*legacyDriverConnection)(nil),
		(*modernDriverConnection)(nil),
		nil,
	} {
		if err := PingProtocol(t.Context(), connection); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("PingProtocol(%T) = %v, want unsupported", connection, err)
		}
	}
}

func TestPingProtocolRejectsUnusableOrCanceledConnection(t *testing.T) {
	connection := &modernDriverConnection{Connection: &modern.Connection{}}
	if err := PingProtocol(t.Context(), connection); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("PingProtocol on a disconnected connection = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := PingProtocol(ctx, connection); !errors.Is(err, context.Canceled) {
		t.Fatalf("PingProtocol on a canceled context = %v", err)
	}
}
