package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"runtime"
	"testing"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	legacyNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	"github.com/marcuoli/go-oracle-session/session"
)

func TestBorrowedSampleProfileRequiresEveryQualifiedIdentityField(t *testing.T) {
	qualified := BorrowedSampleIdentity{
		Version: "23.26.1.0.0", User: "SYSTEM", IsDBA: "FALSE", Container: "CDB$ROOT",
		DatabaseRole: "PRIMARY", OpenMode: "READ WRITE", Protocol: "tcp",
	}
	if !qualified.qualified() {
		t.Fatal("native proof profile declined")
	}
	for _, tc := range []struct {
		name   string
		change func(*BorrowedSampleIdentity)
	}{
		{"other RU", func(id *BorrowedSampleIdentity) { id.Version = "23.26.2.0.0" }},
		{"different account", func(id *BorrowedSampleIdentity) { id.User = "SCOTT" }},
		{"SYSDBA", func(id *BorrowedSampleIdentity) { id.IsDBA = "TRUE" }},
		{"PDB", func(id *BorrowedSampleIdentity) { id.Container = "FREEPDB1" }},
		{"standby", func(id *BorrowedSampleIdentity) { id.DatabaseRole = "PHYSICAL STANDBY" }},
		{"mounted", func(id *BorrowedSampleIdentity) { id.OpenMode = "MOUNTED" }},
		{"other transport", func(id *BorrowedSampleIdentity) { id.Protocol = "tcps" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := qualified
			tc.change(&id)
			if id.qualified() {
				t.Fatal("unqualified profile admitted")
			}
		})
	}
}

type borrowedQualificationForeign struct {
	fixtureConnection
	t *testing.T
}

func (c *borrowedQualificationForeign) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	c.t.Fatal("metadata SQL dispatched for an unqualified provider")
	return nil, ErrUnsupported
}

func (c *borrowedQualificationForeign) Ping(context.Context) error {
	c.t.Fatal("foreign provider ping invoked")
	return ErrUnsupported
}

func TestBorrowedSampleUnsupportedBeforeNativeDispatch(t *testing.T) {
	foreign := &borrowedQualificationForeign{fixtureConnection: fixtureConnection{mode: V2}, t: t}
	connects := 0
	pool := sql.OpenDB(&Connector{mode: V2, legacy: testBackend{func(context.Context) (driver.Conn, error) {
		connects++
		return foreign, nil
	}}})
	t.Cleanup(func() { _ = pool.Close() })
	lease, err := pool.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()

	options := BorrowedSampleOptions{ExclusiveNonTransaction: true, StandaloneOCI: func(driver.Conn) (string, bool, error) {
		t.Fatal("standalone proof consulted for an unrelated driver")
		return "", false, nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		conn    *sql.Conn
		options BorrowedSampleOptions
		want    error
	}{
		{"already canceled", ctx, lease, options, context.Canceled},
		{"nil lease", t.Context(), nil, options, ErrUnsupported},
		{"no transaction assurance", t.Context(), lease, BorrowedSampleOptions{}, ErrUnsupported},
		{"foreign provider", t.Context(), lease, options, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finalize, err := QualifyBorrowedSample(tc.ctx, tc.conn, tc.options)
			if finalize != nil || !errors.Is(err, tc.want) {
				t.Fatalf("original API: finalizer present=%t, error=%v, want %v", finalize != nil, err, tc.want)
			}
			finalize, err = QualifyBorrowedSampleWithIdentity(tc.ctx, tc.conn, tc.options,
				func(context.Context, *sql.Conn) (BorrowedSampleIdentity, error) {
					t.Fatal("identity reader invoked before provider admission")
					return BorrowedSampleIdentity{}, nil
				})
			if finalize != nil || !errors.Is(err, tc.want) {
				t.Fatalf("reader API: finalizer present=%t, error=%v, want %v", finalize != nil, err, tc.want)
			}
		})
	}

	err = lease.Raw(func(raw any) error {
		if raw != foreign || connects != 1 {
			t.Fatal("declined qualification changed or retired the lease")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type borrowedOCIError int

func (err borrowedOCIError) Error() string { return "native OCI decision fixture" }
func (err borrowedOCIError) Code() int     { return int(err) }

func TestBorrowedSampleMetadataFallbackIsProviderSpecific(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		oci  bool
		want bool
	}{
		{"retained legacy version", legacyNetwork.NewOracleError(904), false, true},
		{"standalone OCI missing grant", borrowedOCIError(942), true, true},
		{"standalone OCI denied", borrowedOCIError(1031), true, true},
		{"standalone OCI legacy userenv", borrowedOCIError(2003), true, true},
		{"OCI-shaped error on other provider", borrowedOCIError(904), false, false},
		{"other Oracle failure", borrowedOCIError(3113), true, false},
		{"transport loss", driver.ErrBadConn, true, false},
		{"no observed identity", sql.ErrNoRows, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := borrowedSampleIdentityUnavailable(errors.Join(tc.err), tc.oci); got != tc.want {
				t.Fatalf("unsupported=%t, want %t", got, tc.want)
			}
		})
	}
}

// A disconnected retained connection is enough to exercise the provider gate.
// The finalizer's actual ping is covered by the native compatibility checks.
func TestBorrowedSampleIdentityReaderUsesAdmittedLease(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("retained V2 borrowed sampling is qualified only on Windows amd64")
	}
	qualified := BorrowedSampleIdentity{
		Version: "23.26.1.0.0", User: "SYSTEM", IsDBA: "FALSE", Container: "CDB$ROOT",
		DatabaseRole: "PRIMARY", OpenMode: "READ WRITE", Protocol: "tcp",
	}
	failed := errors.New("identity read failed")
	for _, tc := range []struct {
		name     string
		identity BorrowedSampleIdentity
		readErr  error
		cancel   bool
		want     error
	}{
		{name: "qualified", identity: qualified},
		{name: "unqualified", want: ErrUnsupported},
		{name: "missing row", readErr: sql.ErrNoRows, want: ErrUnsupported},
		{name: "fatal read", readErr: failed, want: failed},
		{name: "canceled session", identity: qualified, cancel: true, want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connects, disposals := 0, 0
			physical := &legacyDriverConnection{
				Connection: &legacy.Connection{},
				health: &connectionHealth{discard: func() error {
					disposals++
					return nil
				}},
			}
			pool := sql.OpenDB(&Connector{mode: V2, legacy: testBackend{func(context.Context) (driver.Conn, error) {
				connects++
				return physical, nil
			}}})
			t.Cleanup(func() { _ = pool.Close() })
			lease, err := pool.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lease.Close() })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			calls := 0
			finalize, err := QualifyBorrowedSampleWithIdentity(ctx, lease, BorrowedSampleOptions{ExclusiveNonTransaction: true},
				func(gotCtx context.Context, gotLease *sql.Conn) (BorrowedSampleIdentity, error) {
					calls++
					if gotCtx != ctx || gotLease != lease {
						t.Fatal("reader did not receive the original context and lease")
					}
					if tc.cancel {
						cancel()
					}
					return tc.identity, tc.readErr
				})
			if calls != 1 || connects != 1 {
				t.Fatalf("identity reads=%d, native admissions=%d, want one each", calls, connects)
			}
			if !errors.Is(err, tc.want) || (finalize != nil) != (tc.want == nil) {
				t.Fatalf("finalizer present=%t, error=%v, want %v", finalize != nil, err, tc.want)
			}
			if tc.readErr == failed && err != failed {
				t.Fatal("fatal reader error identity was changed")
			}
			if finalize != nil {
				health, err := finalize(ctx, new(legacyDriverConnection))
				if health != (session.SampleHealth{}) || !errors.Is(err, session.ErrUnavailable) {
					t.Fatalf("different connection finalization = %+v, %v", health, err)
				}
			}
			err = lease.Raw(func(raw any) error {
				if raw != physical {
					t.Fatal("identity read changed the physical connection")
				}
				return nil
			})
			if tc.cancel {
				if !errors.Is(err, sql.ErrConnDone) || disposals != 1 {
					t.Fatalf("canceled lease Raw=%v and disposals=%d, want retired lease", err, disposals)
				}
			} else if err != nil || disposals != 0 {
				t.Fatalf("lease Raw=%v and disposals=%d before close", err, disposals)
			}
		})
	}
}
