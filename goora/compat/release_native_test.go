package compat_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/goora/compat"
	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-sqltemplate/reference"
)

var nativeReleaseTargets = map[string]string{
	"9.2.0.8": "9", "10.2.0.5": "10", "11.2.0.4": "11", "12.1.0.2": "12", "12.2.0.1": "12",
	"18": "18", "19": "19", "23": "23", "26ai": "23",
}

func nativeReleaseMatches(target, version, banner string) bool {
	parts := strings.Split(version, ".")
	if len(parts) != 5 || parts[0] != nativeReleaseTargets[target] {
		return false
	}
	for _, part := range parts {
		if n, err := strconv.Atoi(part); err != nil || n < 0 {
			return false
		}
	}
	switch target {
	case "18", "19":
		return true
	case "23", "26ai":
		// Oracle AI Database 26ai uses the 23.26 numeric release line.
		minor, _ := strconv.Atoi(parts[1])
		banner = strings.Join(strings.Fields(strings.ToLower(banner)), "")
		if target == "26ai" {
			return minor >= 26 && strings.Contains(banner, "26ai")
		}
		return minor < 26 && (strings.Contains(banner, "23ai") || strings.Contains(banner, "23c"))
	default:
		return strings.Join(parts[:4], ".") == target
	}
}

func TestNativeReleaseMatches(t *testing.T) {
	for _, tc := range []struct {
		target, version, banner string
		want                    bool
	}{
		{"9.2.0.8", "9.2.0.8.0", "", true},
		{"10.2.0.5", "10.2.0.1.0", "", false},
		{"10.2.0.5", "10.2.0.5.0", "", true},
		{"11.2.0.4", "11.2.0.2.0", "", false},
		{"11.2.0.4", "11.2.0.4.0", "", true},
		{"12.1.0.2", "12.1.0.2.0", "", true},
		{"12.2.0.1", "12.2.0.1.0", "", true},
		{"18", "18.4.0.0.0", "", true},
		{"19", "19.31.0.0.0", "", true},
		{"23", "23.9.0.25.07", "Oracle Database 23ai Free", true},
		{"23", "23.26.1.0.0", "Oracle AI Database 26ai", false},
		{"26ai", "23.26.1.0.0", "Oracle AI Database 26ai", true},
		{"26ai", "23.26.1.0.0", "Oracle Database 26 AI", true},
		{"26ai", "23.9.0.25.07", "Oracle Database 23ai Free", false},
		{"26ai", "23.26", "Oracle AI Database 26ai", false},
		{"26ai", "23.26.1.0.0", "Oracle Database 23ai Free", false},
		{"23", "23.9.0.25.07", "", false},
	} {
		if got := nativeReleaseMatches(tc.target, tc.version, tc.banner); got != tc.want {
			t.Errorf("%s / %s: got %t, want %t", tc.target, tc.version, got, tc.want)
		}
	}
}

// TestNativeReleaseMatrixEntry checks one live, approved release target. Private
// input travels only on stdin. SQL is read-only and rollback runs after sampling.
func TestNativeReleaseMatrixEntry(t *testing.T) {
	if os.Getenv("ORACLE_COMPAT_RELEASE_STDIN") != "1" {
		t.Skip("native release checks are opt-in")
	}
	var cfg struct{ DSN, Target, SourceRevision, ProofPath string }
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal("invalid private native release input")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatal("unexpected private native release input suffix")
	}
	revision, err := hex.DecodeString(cfg.SourceRevision)
	if len(revision) != 20 || err != nil || nativeReleaseTargets[cfg.Target] == "" || cfg.DSN == "" || cfg.ProofPath == "" {
		t.Fatal("invalid native release target or source identity")
	}

	build, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("native release test executable has no build metadata")
	}
	settings := make(map[string]string, len(build.Settings))
	for _, setting := range build.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["vcs.revision"] != cfg.SourceRevision || settings["vcs.modified"] != "false" || settings["CGO_ENABLED"] != "0" {
		t.Fatal("native release test executable is not a clean CGO-disabled build of the requested revision")
	}

	proof := map[string]any{
		"target": cfg.Target, "sourceRevision": cfg.SourceRevision, "go": runtime.Version(),
		"os": runtime.GOOS, "arch": runtime.GOARCH, "oracleClient": "not used",
		"checkedUTC": time.Now().UTC(), "completed": false,
	}
	t.Cleanup(func() {
		raw, err := json.MarshalIndent(proof, "", "  ")
		if err != nil || os.WriteFile(cfg.ProofPath, raw, 0600) != nil {
			t.Error("cannot retain native release evidence")
		}
	})
	check := func(stage string, err error) {
		t.Helper()
		if err != nil {
			proof["failedStage"] = stage
			code, _ := compat.OracleCode(err)
			proof["oracleCode"] = code
			t.Fatalf("%s failed: type=%T ORA=%d", stage, err, code)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Second)
	defer cancel()
	connector, err := compat.NewConnector(cfg.DSN, compat.Options{})
	check("connector configuration", err)
	pool := sql.OpenDB(connector)
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	lease, err := pool.Conn(ctx)
	check("initial admission", err)
	defer lease.Close()

	var version, isDBA, banner string
	check("instance identity", lease.QueryRowContext(ctx,
		"SELECT VERSION, SYS_CONTEXT('USERENV','ISDBA') FROM V$INSTANCE").Scan(&version, &isDBA))
	proof["version"] = version
	proof["isDBA"] = isDBA
	short := strings.Split(version, ".")[0]
	if short == nativeReleaseTargets[cfg.Target] {
		switch cfg.Target {
		case "18", "19", "23", "26ai":
			check("full instance version", lease.QueryRowContext(ctx, "SELECT VERSION_FULL FROM V$INSTANCE").Scan(&version))
			proof["version"] = version
		}
	}
	if short == "23" && (cfg.Target == "23" || cfg.Target == "26ai") {
		check("database product branding", lease.QueryRowContext(ctx,
			"SELECT BANNER_FULL FROM V$VERSION WHERE BANNER LIKE 'Oracle%' AND ROWNUM = 1").Scan(&banner))
		proof["banner"] = banner
	}
	if !nativeReleaseMatches(cfg.Target, version, banner) {
		proof["failedStage"] = "version mismatch"
		t.Fatal("observed Oracle patch does not match the required release target")
	}
	var physical driver.Conn
	check("default driver selection", lease.Raw(func(raw any) error {
		selected, ok := raw.(interface{ CompatibilityMode() compat.Mode })
		if !ok || selected.CompatibilityMode() != compat.V2 {
			return errors.New("expected V2 default")
		}
		physical = raw.(driver.Conn)
		proof["mode"] = selected.CompatibilityMode().String()
		return nil
	}))
	var number string
	var null sql.NullString
	check("native exact number and NULL", lease.QueryRowContext(ctx,
		"SELECT 9007199254740993, CAST(NULL AS NUMBER) FROM DUAL").Scan(&number, &null))
	if number != "9007199254740993" || null.Valid {
		t.Fatal("native NUMBER or NULL changed")
	}
	proof["exactNumberAndNull"] = true

	ref, err := reference.NewReference("sdk", "release", "borrowed-sample", 1)
	check("statement reference", err)
	statement := session.Statement{Reference: ref, Text: "SELECT 1 FROM DUAL UNION ALL SELECT 2 FROM DUAL ORDER BY 1"}
	var first int
	read := func(rows *sql.Rows) error {
		if !rows.Next() {
			return errors.New("expected first sample row")
		}
		return rows.Scan(&first)
	}
	sampleErr := session.SampleBorrowed(ctx, lease, statement, read, func(ctx context.Context, raw any) (session.SampleHealth, error) {
		if raw != physical {
			return session.SampleHealth{}, errors.New("sample finalizer changed physical connection")
		}
		err := compat.PingProtocol(ctx, physical)
		// The fixture is standalone and no transaction or DML is active.
		// A post-return rollback checks transaction usability on the same lease.
		return session.SampleHealth{ProtocolSettled: err == nil, TransactionUsable: err == nil}, err
	})
	if first != 1 {
		t.Fatal("unexpected partial-sample value")
	}
	if cfg.Target == "9.2.0.8" && errors.Is(sampleErr, compat.ErrUnsupported) {
		proof["sample"] = "unsupported native ping; lease retired"
	} else {
		check("borrowed sample", sampleErr)
		check("same lease after sample", lease.Raw(func(raw any) error {
			if raw != physical {
				return errors.New("same lease proof failed")
			}
			return nil
		}))
		tx, err := lease.BeginTx(ctx, nil)
		check("begin after sample", err)
		check("rollback after sample", tx.Rollback())
		proof["sample"] = "same lease and rollback"
		first = 0
		unqualifiedCalls := 0
		sampleErr = session.SampleBorrowed(ctx, lease, statement, read, func(context.Context, any) (session.SampleHealth, error) {
			unqualifiedCalls++
			return session.SampleHealth{}, nil
		})
		if first != 1 || unqualifiedCalls != 1 {
			t.Fatal("missing-proof finalizer did not run after reading and closing the cursor")
		}
	}
	if !errors.Is(sampleErr, session.ErrUnavailable) ||
		!errors.Is(lease.Raw(func(any) error { return nil }), sql.ErrConnDone) {
		t.Fatal("unproven native connection returned to the caller")
	}
	proof["unprovenLeaseRetired"] = true
	replacement, err := pool.Conn(ctx)
	check("fresh admission after retirement", err)
	defer replacement.Close()
	check("fresh physical connection", replacement.Raw(func(raw any) error {
		if raw == physical {
			return errors.New("retired connection reused by the pool")
		}
		return nil
	}))
	var one int
	check("fresh query after retirement", replacement.QueryRowContext(ctx, "SELECT 1 FROM DUAL").Scan(&one))
	if one != 1 {
		t.Fatal("fresh connection returned an unexpected value")
	}
	proof["completed"] = true
}
