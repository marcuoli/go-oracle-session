package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	legacyNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
)

func TestBorrowedSampleProfileRequiresEveryQualifiedIdentityField(t *testing.T) {
	qualified := borrowedSampleIdentity{
		version: "23.26.1.0.0", user: "SYSTEM", isDBA: "FALSE", container: "CDB$ROOT",
		databaseRole: "PRIMARY", openMode: "READ WRITE", protocol: "tcp",
	}
	if !qualified.qualified() {
		t.Fatal("native proof profile declined")
	}
	for _, tc := range []struct {
		name   string
		change func(*borrowedSampleIdentity)
	}{
		{"other RU", func(id *borrowedSampleIdentity) { id.version = "23.26.2.0.0" }},
		{"different account", func(id *borrowedSampleIdentity) { id.user = "SCOTT" }},
		{"SYSDBA", func(id *borrowedSampleIdentity) { id.isDBA = "TRUE" }},
		{"PDB", func(id *borrowedSampleIdentity) { id.container = "FREEPDB1" }},
		{"standby", func(id *borrowedSampleIdentity) { id.databaseRole = "PHYSICAL STANDBY" }},
		{"mounted", func(id *borrowedSampleIdentity) { id.openMode = "MOUNTED" }},
		{"other transport", func(id *borrowedSampleIdentity) { id.protocol = "tcps" }},
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
				t.Fatalf("finalizer present=%t, error=%v, want %v", finalize != nil, err, tc.want)
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
