package compat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/goora/compat"
)

type nativeDialCounter struct{ calls atomic.Int32 }

func (d *nativeDialCounter) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.calls.Add(1)
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

// TestNativeCompatibility is opt-in and accepts private input only over stdin.
// It changes session-local/temporary state; never create users, grants or objects.
// Container projection is explicitly optional so the baseline query remains
// independent of modern CDB metadata. This harness is not a query catalog.
func TestNativeCompatibility(t *testing.T) {
	if os.Getenv("ORACLE_COMPAT_NATIVE_STDIN") != "1" {
		t.Skip("native compatibility not enabled")
	}
	var cfg struct {
		DSN, ProofPath, SourceRevision string
		Mode                           compat.Mode
		Container, ExtendedVersion     bool
		Expected                       struct{ User, Database, Container, Version, IsDBA string }
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal("invalid private native input")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("unexpected private input suffix")
	}
	dialer := &nativeDialCounter{}
	connector, err := compat.NewConnector(cfg.DSN, compat.Options{Mode: cfg.Mode, Dialer: dialer, LoginWarningHandler: func(string) bool { return true }})
	if err != nil {
		t.Fatal("invalid private connector configuration")
	}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	pool.SetMaxOpenConns(4)
	pool.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	type identity struct{ User, Database, Container, Version, VersionFull, IsDBA, Selected string }
	observe := func(lease *sql.Conn) (identity, error) {
		var result identity
		err := lease.QueryRowContext(ctx, "SELECT USER,(SELECT NAME FROM V$DATABASE),VERSION,SYS_CONTEXT('USERENV','ISDBA') FROM V$INSTANCE").Scan(&result.User, &result.Database, &result.Version, &result.IsDBA)
		if err != nil {
			return result, err
		}
		if cfg.ExtendedVersion {
			// VERSION_FULL is an explicitly selected18c+ qualification projection.
			if err := lease.QueryRowContext(ctx, "SELECT VERSION_FULL FROM V$INSTANCE").Scan(&result.VersionFull); err != nil {
				return result, err
			}
		}
		if cfg.Container {
			if err := lease.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV','CON_NAME') FROM DUAL").Scan(&result.Container); err != nil {
				return result, err
			}
		}
		err = lease.Raw(func(raw any) error {
			selected, ok := raw.(interface{ CompatibilityMode() compat.Mode })
			if !ok {
				return fmt.Errorf("selected mode unavailable")
			}
			result.Selected = selected.CompatibilityMode().String()
			return nil
		})
		return result, err
	}
	safeFailure := func(phase string, err error) {
		t.Helper()
		code, _ := compat.OracleCode(err)
		t.Fatalf("%s failed: type=%T ORA=%d", phase, err, code)
	}
	var observations []identity
	checkIdentity := func(value identity) bool {
		expected := cfg.Expected
		return (expected.User == "" || value.User == expected.User) && (expected.Database == "" || value.Database == expected.Database) && (expected.Container == "" || value.Container == expected.Container) && (expected.Version == "" || value.Version == expected.Version) && (expected.IsDBA == "" || value.IsDBA == expected.IsDBA) && value.Selected == cfg.Mode.String()
	}
	// Holding all leases forces distinct physical admissions through one factory.
	leases := make([]*sql.Conn, 4)
	var group sync.WaitGroup
	failures := make(chan error, 4)
	for i := range leases {
		group.Go(func() {
			lease, err := pool.Conn(ctx)
			if err != nil {
				failures <- err
				return
			}
			leases[i] = lease
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		safeFailure("concurrent admission", err)
	}
	defer func() {
		for _, lease := range leases {
			if lease != nil {
				lease.Close()
			}
		}
	}()
	for _, lease := range leases {
		value, err := observe(lease)
		if err != nil {
			safeFailure("identity", err)
		}
		observations = append(observations, value)
	}
	// Retain observations even when caller-supplied identity expectations fail.
	proof := map[string]any{"checkedUTC": time.Now().UTC(), "sourceRevision": cfg.SourceRevision, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "mode": cfg.Mode.String(), "identities": observations, "physicalDials": dialer.calls.Load(), "patchInventory": "Not queried", "scope": "Four concurrent physical admissions, identity, repeated prepared OUT, exact scalar/NULL/Unicode and temporary CLOB only"}
	writeProof := func() {
		t.Helper()
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.ProofPath, data, 0600); err != nil {
			t.Fatal("cannot retain native proof")
		}
	}
	writeProof()
	for _, value := range observations {
		if !checkIdentity(value) {
			t.Fatal("identity differs from supplied expectation; observed proof retained")
		}
	}
	if dialer.calls.Load() != 4 {
		t.Fatal("unexpected physical admission count")
	}
	lease := leases[0]
	var number, text string
	var null sql.NullString
	if err := lease.QueryRowContext(ctx, "SELECT TO_CHAR(9007199254740993),CAST(NULL AS VARCHAR2(1)),'é漢' FROM DUAL").Scan(&number, &null, &text); err != nil {
		safeFailure("exact scalar", err)
	}
	if number != "9007199254740993" || null.Valid || text != "é漢" {
		t.Fatal("exact scalar/NULL/Unicode mismatch")
	}
	statement, err := lease.PrepareContext(ctx, "BEGIN :1 := :2; :3 := TO_CHAR(9007199254740993); :4 := NULL; END;")
	if err != nil {
		safeFailure("prepare OUT", err)
	}
	for i := range 3 {
		text, number = "", ""
		null = sql.NullString{String: "previous", Valid: true}
		input := fmt.Sprintf("é漢-%d", i)
		if _, err := statement.ExecContext(ctx, compat.Out{Dest: &text, Size: 32767}, input, compat.Out{Dest: &number, Size: 64}, compat.Out{Dest: &null, Size: 64}); err != nil {
			statement.Close()
			safeFailure("repeated prepared OUT", err)
		}
		if text != input || number != "9007199254740993" || null.Valid {
			statement.Close()
			t.Fatal("OUT capacity/value/NULL mismatch")
		}
	}
	if err := statement.Close(); err != nil {
		safeFailure("prepared close", err)
	}
	statement, err = lease.PrepareContext(ctx, "DECLARE c CLOB; v VARCHAR2(32767) := :1; BEGIN DBMS_LOB.CREATETEMPORARY(c,TRUE,DBMS_LOB.SESSION); FOR i IN 1..25 LOOP DBMS_LOB.WRITEAPPEND(c,LENGTH(v),v); END LOOP; :2 := c; END;")
	if err != nil {
		safeFailure("prepare temporary CLOB", err)
	}
	var clob compat.Clob
	input := strings.Repeat("é漢", 1000)
	if _, err := statement.ExecContext(ctx, input, compat.Out{Dest: &clob}); err != nil {
		statement.Close()
		safeFailure("temporary CLOB OUT", err)
	}
	if err := clob.Read(ctx); err != nil {
		statement.Close()
		safeFailure("temporary CLOB read", err)
	}
	value, err := clob.Value()
	if err != nil {
		clob.Free()
		statement.Close()
		safeFailure("temporary CLOB value", err)
	}
	if value != strings.Repeat(input, 25) {
		clob.Free()
		statement.Close()
		t.Fatal("temporary CLOB exact value mismatch")
	}
	if err := clob.Free(); err != nil {
		statement.Close()
		safeFailure("temporary CLOB free", err)
	}
	if err := statement.Close(); err != nil {
		safeFailure("temporary CLOB statement close", err)
	}
	proof["exactValues"] = true
	proof["preparedOUTExecutions"] = 3
	proof["temporaryClobBytes"] = len(strings.Repeat(input, 25))
	proof["completed"] = true
	writeProof()
	t.Log("bounded native compatibility checks passed; scope and untested conditions retained")
}
