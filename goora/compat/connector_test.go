package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"testing"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
)

type testBackend struct {
	connect func(context.Context) (driver.Conn, error)
}

func (b testBackend) Connect(ctx context.Context) (driver.Conn, error) { return b.connect(ctx) }
func (testBackend) Driver() driver.Driver                              { return nil }

func TestSelectionNeverRetriesFailures(t *testing.T) {
	auth := &modernNetwork.OracleError{ErrCode: 1017}
	cleanup := errors.New("close failure")
	for _, tc := range []struct {
		name           string
		mode           Mode
		cause          error
		wantV2, wantV3 int
	}{
		{"default", V2, nil, 1, 0}, {"modern", V3, nil, 0, 1},
		{"negotiated modern", Negotiated, nil, 0, 1},
		{"legacy signal", Negotiated, modern.ErrLegacyServer, 1, 1},
		{"wrapped signal", Negotiated, fmt.Errorf("capability: %w", modern.ErrLegacyServer), 1, 1},
		{"joined cleanup", Negotiated, errors.Join(modern.ErrLegacyServer, cleanup), 0, 1},
		{"authentication", Negotiated, auth, 0, 1}, {"transport", Negotiated, io.EOF, 0, 1},
		{"deadline", Negotiated, context.DeadlineExceeded, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			counts := [2]int{}
			c := &Connector{mode: tc.mode}
			c.legacy = testBackend{func(got context.Context) (driver.Conn, error) {
				counts[0]++
				if got != ctx {
					t.Fatal("legacy context replaced")
				}
				return &fixtureConnection{mode: V2}, nil
			}}
			c.modern = testBackend{func(got context.Context) (driver.Conn, error) {
				counts[1]++
				if got != ctx {
					t.Fatal("modern context replaced")
				}
				if tc.cause == nil {
					return &fixtureConnection{mode: V3}, nil
				}
				return nil, tc.cause
			}}
			connection, err := c.Connect(ctx)
			if tc.cause == nil || tc.wantV2 > 0 {
				want := V3
				if tc.wantV2 > 0 {
					want = V2
				}
				got, ok := ConnectionMode(connection)
				if err != nil || !ok || got != want {
					t.Fatalf("successful selection mode=%v ok=%v error=%v", got, ok, err)
				}
			}
			if counts[0] != tc.wantV2 || counts[1] != tc.wantV3 {
				t.Fatalf("attempts=%v", counts)
			}
			if tc.wantV2 == 0 && tc.cause != nil && !errors.Is(err, tc.cause) {
				t.Fatalf("original cause lost: %v", err)
			}
		})
	}
}

func TestCancelledCapabilityNeverStartsLegacy(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	c := &Connector{mode: Negotiated}
	c.modern = testBackend{func(context.Context) (driver.Conn, error) { cancel(); return nil, modern.ErrLegacyServer }}
	c.legacy = testBackend{func(context.Context) (driver.Conn, error) {
		t.Fatal("second admission after cancellation")
		return nil, nil
	}}
	_, err := c.Connect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cause=%v", err)
	}
}

func TestPanicNeverSelectsAnotherDriver(t *testing.T) {
	c := &Connector{mode: Negotiated}
	c.modern = testBackend{func(context.Context) (driver.Conn, error) { panic("synthetic driver fault") }}
	c.legacy = testBackend{func(context.Context) (driver.Conn, error) { t.Fatal("panic fallback"); return nil, nil }}
	defer func() {
		if recover() == nil {
			t.Fatal("unexpected panic conversion")
		}
	}()
	_, _ = c.Connect(t.Context())
}

func TestCompatibilityFactoryAndRegistration(t *testing.T) {
	if !slices.Contains(sql.Drivers(), DriverName) {
		t.Fatal("compatibility driver not registered")
	}
	for _, mode := range []Mode{V2, V3, Negotiated, Automatic} {
		c, err := NewConnector("oracle://fixture:unused@127.0.0.1:1/fixture", Options{Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if !IsDriver(c.Driver()) || IsDriver(nil) {
			t.Fatal("pool driver identification")
		}
		if c.Mode() != mode {
			t.Fatal("configured policy changed")
		}
	}
	if _, err := NewConnector("", Options{Mode: Mode(255)}); !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("invalid policy=%v", err)
	}
}

func TestDriverURLPolicyPreservesIdentity(t *testing.T) {
	input := "oracle://user:p%3Ass%2B%E6%BC%A2@fixture.invalid:1521/service%2Fname?fAsT+LoGiN=TRUE&FAST+LOGIN=DISABLE&lob+fetch=pre&x=a%2Bb&x=a+b"
	original, err := url.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []Mode{V2, V3} {
		got, err := prepareDSN(input, mode)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.Parse(got)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Host != original.Host || parsed.User.String() != original.User.String() || parsed.EscapedPath() != original.EscapedPath() {
			t.Fatal("identity changed")
		}
		if !slices.Equal(parsed.Query()["x"], original.Query()["x"]) || parsed.Query().Get("lob fetch") != "pre" {
			t.Fatal("caller options lost")
		}
		want := ""
		if mode == V3 {
			want = "FALSE"
		}
		if parsed.Query().Get("FAST LOGIN") != want || parsed.Query().Has("fAsT LoGiN") {
			t.Fatalf("policy=%s", parsed.RawQuery)
		}
	}
	if _, err := prepareDSN("oracle://u:p@host/service?x=%zz", V2); !errors.Is(err, ErrInvalidDSN) {
		t.Fatalf("malformed options=%v", err)
	}
}

type fixtureConnection struct {
	mode   Mode
	closed int
}

func (c *fixtureConnection) Prepare(string) (driver.Stmt, error) { return nil, ErrUnsupported }
func (c *fixtureConnection) Begin() (driver.Tx, error)           { return nil, ErrUnsupported }
func (c *fixtureConnection) Close() error                        { c.closed++; return nil }
func (c *fixtureConnection) CompatibilityMode() Mode             { return c.mode }

func TestReturnedConnectionErrorIsDisposedWithoutFallback(t *testing.T) {
	physical := &fixtureConnection{mode: V3}
	c := &Connector{mode: Negotiated}
	c.modern = testBackend{func(context.Context) (driver.Conn, error) { return physical, modern.ErrLegacyServer }}
	c.legacy = testBackend{func(context.Context) (driver.Conn, error) {
		t.Fatal("connection-bearing failure selected legacy")
		return &fixtureConnection{mode: V2}, nil
	}}
	conn, err := c.Connect(t.Context())
	if conn != nil || !errors.Is(err, modern.ErrLegacyServer) || physical.closed != 1 {
		t.Fatalf("conn=%v err=%v closes=%d", conn, err, physical.closed)
	}
}
