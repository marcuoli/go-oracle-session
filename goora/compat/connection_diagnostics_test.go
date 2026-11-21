package compat

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"testing"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
)

type failingDiagnosticDialer struct{ calls int }

func (d *failingDiagnosticDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls++
	return nil, io.EOF
}

func TestConnectWithDiagnosticsRecordsNativeV3Failure(t *testing.T) {
	dialer := &failingDiagnosticDialer{}
	connector, err := NewConnector("oracle://reader:secret@fixture.invalid:1521/service", Options{
		Mode: Negotiated, Dialer: dialer,
	})
	if err != nil {
		t.Fatal(err)
	}

	conn, attempts, err := connector.ConnectWithDiagnostics(t.Context())
	if conn != nil || err == nil || dialer.calls != 1 || len(attempts) != 1 {
		t.Fatalf("expected one failed V3 admission, got connection=%v error=%v attempts=%+v dials=%d",
			conn, err, attempts, dialer.calls)
	}
	attempt := attempts[0]
	if attempt.Mode != V3 || attempt.Connected || attempt.Handshake == nil ||
		attempt.Handshake.Phase != "tns_connect" || attempt.Handshake.AuthenticationCompleted {
		t.Fatalf("unexpected V3 observation: %+v (handshake %+v)", attempt, attempt.Handshake)
	}
}

func TestConnectWithDiagnosticsKeepsSelectionAndErrors(t *testing.T) {
	auth := errors.New("authentication failure")
	closeFailure := errors.New("cleanup failure")
	for _, tc := range []struct {
		name  string
		mode  Mode
		cause error
		v2    bool
	}{
		{"default V2", V2, nil, true},
		{"clean legacy capability", Negotiated, modern.ErrLegacyServer, true},
		{"cleanup failure", Negotiated, errors.Join(modern.ErrLegacyServer, closeFailure), false},
		{"authentication failure", Negotiated, auth, false},
		{"generic EOF", Negotiated, io.EOF, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := &fixtureConnection{mode: V2}
			c := &Connector{
				mode: tc.mode,
				legacy: testBackend{func(context.Context) (driver.Conn, error) {
					return legacy, nil
				}},
				modern: testBackend{func(context.Context) (driver.Conn, error) {
					return nil, tc.cause
				}},
			}
			conn, attempts, err := c.ConnectWithDiagnostics(t.Context())
			if tc.v2 {
				if conn != legacy || err != nil {
					t.Fatalf("legacy connection changed: %v %v", conn, err)
				}
			} else if conn != nil || err != tc.cause {
				t.Fatalf("original failure changed: %v %v", conn, err)
			}
			want := 1
			if tc.mode == Negotiated && tc.v2 {
				want = 2
			}
			if len(attempts) != want {
				t.Fatalf("observed %d attempts, want %d", len(attempts), want)
			}
			if tc.mode == Negotiated && (attempts[0].Mode != V3 || attempts[0].Connected) {
				t.Fatalf("first attempt should be failed V3: %+v", attempts[0])
			}
			if tc.v2 {
				last := attempts[len(attempts)-1]
				if last.Mode != V2 || !last.Connected || last.Handshake != nil {
					t.Fatalf("V2 should report admission without native V3 state: %+v", last)
				}
			} else if attempts[0].Connected {
				t.Fatalf("failure reported as connected: %+v", attempts[0])
			}
		})
	}
}

func TestConnectWithDiagnosticsCancelledBeforeAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	conn, attempts, err := (&Connector{mode: V2}).ConnectWithDiagnostics(ctx)
	if conn != nil || len(attempts) != 0 || err != context.Canceled {
		t.Fatalf("cancelled context admitted work: connection=%v attempts=%+v error=%v", conn, attempts, err)
	}
}
