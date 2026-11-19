package compat

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"testing"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
)

type rejectedAdmission struct {
	calls int
	cause error
}

func (d *rejectedAdmission) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, d.cause
}

func TestEveryPhysicalAdmissionUsesFreshNativeState(t *testing.T) {
	for _, mode := range []Mode{V2, V3} {
		t.Run(mode.String(), func(t *testing.T) {
			factoryCalls := 0
			dialer := &rejectedAdmission{cause: errors.New("synthetic admission failure")}
			connector, err := NewConnector("oracle://fixture:unused@127.0.0.1:1/fixture", Options{Mode: mode, Dialer: dialer})
			if err != nil {
				t.Fatal(err)
			}
			seen := map[driver.Driver]bool{}
			if mode == V2 {
				backend := connector.legacy.(*legacyDriverConnector)
				original := backend.fresh
				if original == nil {
					t.Fatal("shared native state")
				}
				backend.fresh = func() *legacy.OracleConnector {
					factoryCalls++
					created := original()
					if seen[created.Driver()] {
						t.Error("native registry reused")
					}
					seen[created.Driver()] = true
					return created
				}
			} else {
				backend := connector.modern.(*modernDriverConnector)
				original := backend.fresh
				if original == nil {
					t.Fatal("shared native state")
				}
				backend.fresh = func() *modernDriverConnector {
					factoryCalls++
					created := original()
					if seen[created.Driver()] {
						t.Error("native registry reused")
					}
					seen[created.Driver()] = true
					return created
				}
			}
			for range 3 {
				if connection, err := connector.Connect(t.Context()); connection != nil || !errors.Is(err, dialer.cause) {
					t.Fatalf("admission returned %T %v", connection, err)
				}
			}
			if factoryCalls != 3 || dialer.calls != 3 || len(seen) != 3 {
				t.Fatalf("factories=%d dials=%d registries=%d", factoryCalls, dialer.calls, len(seen))
			}
		})
	}
}
