//go:build integration

package oracle

import (
	"context"
	"database/sql"

	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-oracle-session/transport"
	"github.com/marcuoli/go-sqltemplate/reference"
)

type nativeConfig struct {
	DSN                 string
	Driver              string
	ExpectedVersion     string
	ExpectedFullVersion string
	FixtureUser         string
	FixtureToken        string
	AllowFixtureWrites  bool
}

func statement(t *testing.T, name, text string, args ...any) session.Statement {
	t.Helper()
	ref, err := reference.NewReference("qualification", "oracle", name, 1)
	if err != nil {
		t.Fatal("invalid fixed test reference")
	}
	return session.Statement{Reference: ref, Text: text, Args: args}
}

// Driver error chains can contain connection secrets; native diagnostics retain
// only fixed operation names, types, Oracle codes and terminal-condition flags,
// never DSNs or underlying messages.
func requireOK(t *testing.T, operation string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s failed (%T, %s, EOF=%t, socketClosed=%t, canceled=%t, deadline=%t)", operation, err, regexp.MustCompile(`ORA-[0-9]{5}`).FindString(err.Error()), errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded))
	}
}

func openSession(t *testing.T, cfg nativeConfig, prepared bool, expectDisposed bool) *session.Session {
	t.Helper()
	sockets, err := transport.New(10 * time.Second)
	requireOK(t, "transport", err)
	if cfg.Driver != nativeDriver {
		_ = sockets.Close()
		t.Fatal("native driver differs from the explicitly built qualification target")
	}
	connector := nativeConnector(cfg.DSN, sockets)
	s, err := session.Open(context.Background(), connector, session.Options{
		PreparedContext: prepared, ConnectTimeout: 10 * time.Second,
		QueryTimeout: 10 * time.Second, CleanupTimeout: 3 * time.Second,
		MaxRows: 20, MaxBytes: 16384, Dispose: sockets.Close,
	})
	if err != nil {
		_ = sockets.Close()
		requireOK(t, "admission", err)
	}
	t.Cleanup(func() {
		err := s.Close(context.Background())
		if expectDisposed && onlyClosedSocket(err) {
			t.Log("close retained the expected already-disposed socket error")
			return
		}
		requireOK(t, "session close", err)
	})
	if scalar(t, s, "fixture-user", "select user from dual") != cfg.FixtureUser ||
		scalar(t, s, "fixture-marker", "select token from native_fixture") != cfg.FixtureToken {
		t.Fatal("fresh session did not admit the exact dedicated per-run fixture")
	}
	version := scalar(t, s, "admission-version", "select version from v$instance")
	if !strings.HasPrefix(version, cfg.ExpectedVersion) {
		t.Fatal("fresh session has a different server version")
	}
	if cfg.ExpectedFullVersion != "" && scalar(t, s, "admission-full-version", "select version_full from v$instance") != cfg.ExpectedFullVersion {
		t.Fatal("fresh session has a different full server release")
	}
	return s
}

func scalar(t *testing.T, q interface {
	Query(context.Context, session.Statement) (session.Result, error)
}, name, text string, args ...any) string {
	t.Helper()
	result, err := q.Query(context.Background(), statement(t, name, text, args...))
	requireOK(t, name, err)
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0].Null {
		t.Fatalf("%s did not produce one non-NULL scalar", name)
	}
	return result.Rows[0][0].Text
}

// TestNativeSession requires an explicitly writable disposable schema with a
// matching NATIVE_FIXTURE token, NATIVE_TX and
// explicit SELECT on SYS.V_$INSTANCE plus EXECUTE on SYS.DBMS_LOCK. The harness
// verifies the per-run schema before operations and never provisions users or
// changes grants. Read the
// configuration from private stdin only when explicitly enabled by the caller.
func TestNativeSession(t *testing.T) {
	if os.Getenv("ORACLE_TEST_CONFIG_STDIN") != "1" {
		t.Skip("native qualification requires explicit private stdin configuration")
	}
	var cfg nativeConfig
	if err := json.NewDecoder(os.Stdin).Decode(&cfg); err != nil || cfg.DSN == "" || cfg.ExpectedVersion == "" || cfg.FixtureUser == "" || cfg.FixtureToken == "" || !cfg.AllowFixtureWrites {
		t.Fatal("invalid private native configuration")
	}
	for _, mode := range []struct {
		name     string
		prepared bool
		rowID    int
	}{{"direct", false, 1}, {"prepared", true, 2}} {
		t.Run(mode.name, func(t *testing.T) {
			s := openSession(t, cfg, mode.prepared, false)
			if scalar(t, s, "fixture-user", "select user from dual") != cfg.FixtureUser ||
				scalar(t, s, "fixture-marker", "select token from native_fixture") != cfg.FixtureToken {
				t.Fatal("dedicated per-run fixture admission was not proven")
			}
			if cfg.ExpectedFullVersion != "" && scalar(t, s, "full-version", "select version_full from v$instance") != cfg.ExpectedFullVersion {
				t.Fatal("full server release differs from the requested target")
			}
			version := scalar(t, s, "version", "select version from v$instance")
			if !strings.HasPrefix(version, cfg.ExpectedVersion) {
				t.Fatal("server numeric version differs from the requested target")
			}
			t.Logf("server=%s driver=%s mode=%s", version, cfg.Driver, mode.name)
			t.Run("exact-values", func(t *testing.T) {
				result, err := s.Query(context.Background(), statement(t, "exact-values", `select to_char(123456789012345678901234567890,'FM99999999999999999999999999999999999999'),cast(null as varchar2(10)),unistr('\03A9\00E9') from dual`))
				requireOK(t, "exact values", err)
				if len(result.Rows) != 1 || len(result.Rows[0]) != 3 {
					t.Fatal("unexpected exact result shape")
				}
				row := result.Rows[0]
				if row[0].Null || row[0].Text != "123456789012345678901234567890" || !row[1].Null || row[2].Null || row[2].Text != "Ωé" {
					t.Fatal("precision, NULL or character contract failed")
				}
			})
			t.Run("untyped-null-framing", func(t *testing.T) {
				isolated := openSession(t, cfg, mode.prepared, false)
				result, err := isolated.Query(context.Background(), statement(t, "untyped-null", "select null, 'after-null', null from dual"))
				requireOK(t, "untyped NULL framing", err)
				if len(result.Rows) != 1 || len(result.Rows[0]) != 3 || !result.Rows[0][0].Null || result.Rows[0][1].Null || result.Rows[0][1].Text != "after-null" || !result.Rows[0][2].Null {
					t.Fatal("untyped NULL consumed or changed an adjacent column")
				}
			})
			t.Run("character-output-capacity", func(t *testing.T) {
				for _, tc := range []struct {
					name, value string
					capacity    int
				}{
					{"short-wide", "Ωé", 32},
					{"large-declared-capacity", "Ωé", 32767},
					{"multibyte-capacity", strings.Repeat("Ω", 2048), 2048},
				} {
					t.Run(tc.name, func(t *testing.T) {
						isolated := openSession(t, cfg, mode.prepared, false)
						var output string
						_, err := isolated.Exec(context.Background(), statement(t, "character-output", "begin :output := :input; end;", sql.Named("output", nativeCharacterOutput(&output, tc.capacity)), sql.Named("input", tc.value)), session.ExecOptions{})
						requireOK(t, "character output capacity", err)
						if output != tc.value {
							t.Fatal("character output value/character-versus-byte capacity was not preserved")
						}
					})
				}
			})
			t.Run("repeated-output-binds", func(t *testing.T) {
				for _, input := range []int{41, 41, 42, 43} {
					var output int
					result, err := s.Exec(context.Background(), statement(t, "output-binds", "begin :output := :input; end;", sql.Named("output", sql.Out{Dest: &output}), sql.Named("input", input)), session.ExecOptions{})
					requireOK(t, "repeated output bind", err)
					if result == nil || output != input {
						t.Fatal("output bind/result not preserved")
					}
				}
			})
			t.Run("stream-eof", func(t *testing.T) {
				rows, err := s.Stream(context.Background(), statement(t, "stream", "select '1' n from dual union all select '2' from dual union all select '3' from dual"), session.StreamOptions{MaxRows: 3})
				requireOK(t, "stream", err)
				defer rows.Close()
				seen := map[string]int{}
				for rows.Next() {
					var value string
					requireOK(t, "native scan", rows.Scan(&value))
					seen[value]++
				}
				requireOK(t, "stream terminal", rows.Err())
				requireOK(t, "stream close", rows.Close())
				if !rows.Complete() || len(seen) != 3 || seen["1"] != 1 || seen["2"] != 1 || seen["3"] != 1 {
					t.Fatal("native EOF/completeness contract failed")
				}
			})
			t.Run("transaction-outcomes", func(t *testing.T) {
				count := func() string {
					return scalar(t, s, "count", "select to_char(count(*)) from native_tx where id=:id", sql.Named("id", mode.rowID))
				}
				if count() != "0" {
					t.Fatal("disposable fixture row is not initially empty")
				}
				for _, commit := range []bool{false, true} {
					tx, err := s.BeginTx(context.Background(), session.TxOptions{})
					requireOK(t, "begin transaction", err)
					defer tx.Rollback(context.Background())
					_, err = tx.Exec(context.Background(), statement(t, "insert", "insert into native_tx(id,payload) values(:id,:value)", sql.Named("id", mode.rowID), sql.Named("value", "qualification")), session.ExecOptions{})
					requireOK(t, "transaction insert", err)
					if scalar(t, tx, "transaction-count", "select to_char(count(*)) from native_tx where id=:id", sql.Named("id", mode.rowID)) != "1" {
						t.Fatal("insert is not visible inside its own transaction")
					}
					rows, err := tx.Stream(context.Background(), statement(t, "transaction-stream", "select payload from native_tx where id=:id", sql.Named("id", mode.rowID)), session.StreamOptions{})
					requireOK(t, "transaction stream", err)
					if !rows.Next() {
						_ = rows.Close()
						t.Fatal("transaction stream lost its inserted row")
					}
					var payload string
					requireOK(t, "transaction native scan", rows.Scan(&payload))
					if payload != "qualification" {
						_ = rows.Close()
						t.Fatal("transaction stream changed its own inserted value")
					}
					if rows.Next() {
						_ = rows.Close()
						t.Fatal("transaction stream returned duplicate rows")
					}
					requireOK(t, "transaction stream terminal", rows.Err())
					requireOK(t, "transaction stream close", rows.Close())
					if !rows.Complete() {
						t.Fatal("transaction cursor did not complete before finalization")
					}
					if commit {
						requireOK(t, "commit", tx.Commit(context.Background()))
						if tx.Outcome() != session.TxCommitted {
							t.Fatal("commit outcome not confirmed")
						}
					} else {
						requireOK(t, "rollback", tx.Rollback(context.Background()))
						if tx.Outcome() != session.TxRolledBack {
							t.Fatal("rollback outcome not confirmed")
						}
					}
					expected := "0"
					if commit {
						expected = "1"
					}
					if count() != expected {
						t.Fatal("transaction visibility differs from terminal outcome")
					}
				}
			})
			t.Run("early-stream-retirement", func(t *testing.T) {
				isolated := openSession(t, cfg, mode.prepared, true)
				rows, err := isolated.Stream(context.Background(), statement(t, "early-stream", "select 'a' from dual union all select 'b' from dual"), session.StreamOptions{})
				requireOK(t, "early stream", err)
				if !rows.Next() {
					t.Fatal("stream produced no first row")
				}
				if err := rows.Close(); err == nil || rows.Complete() {
					t.Fatal("early close incorrectly established completeness")
				}
				_, err = isolated.Query(context.Background(), statement(t, "retired-query", "select 'x' from dual"))
				if !errors.Is(err, session.ErrUnavailable) {
					t.Fatal("early-closed session was not retired")
				}
			})
			t.Run("active-cancellation", func(t *testing.T) {
				isolated := openSession(t, cfg, mode.prepared, true)
				ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
				defer cancel()
				started := time.Now()
				_, err := isolated.Exec(ctx, statement(t, "cancel", "begin dbms_lock.sleep(5); end;"), session.ExecOptions{})
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("active deadline was not retained (%T)", err)
				}
				if time.Since(started) > 3*time.Second {
					t.Fatal("active cancellation exceeded the qualified client bound")
				}
				_, err = isolated.Query(context.Background(), statement(t, "cancel-retired", "select 'x' from dual"))
				if !errors.Is(err, session.ErrUnavailable) {
					t.Fatal("cancelled session was not retired")
				}
				t.Log("client operation joined and session retired; server termination requires separate observation")
			})
		})
	}
}

// Disposal intentionally closes the physical socket before the driver's later
// logoff. Accept only that terminal close condition, preserving other failures
// even when database/sql or the library has joined them with a socket error.
func onlyClosedSocket(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyClosedSocket(child) {
				return false
			}
		}
		return true
	}
	if child := errors.Unwrap(err); child != nil {
		return onlyClosedSocket(child)
	}
	return errors.Is(err, net.ErrClosed)
}

func TestDisposedCloseClassification(t *testing.T) {
	unexpected := errors.New("unexpected cleanup failure")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"closed", net.ErrClosed, true},
		{"wrapped-closed", &net.OpError{Op: "write", Net: "tcp", Err: net.ErrClosed}, true},
		{"joined-closed", errors.Join(net.ErrClosed, net.ErrClosed), true},
		{"joined-unexpected", errors.Join(net.ErrClosed, unexpected), false},
		{"unexpected", unexpected, false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := onlyClosedSocket(tc.err); got != tc.want {
				t.Fatalf("closed-only=%t, want %t", got, tc.want)
			}
		})
	}
}
