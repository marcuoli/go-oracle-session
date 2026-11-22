package compat_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/marcuoli/go-oracle-session/diagnostic"
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
		OutCharacters                  int
		Mode                           compat.Mode
		Container, ExtendedVersion     bool
		StrictNetworkEncryption        bool
		Expected                       struct{ User, Database, Container, Version, VersionFull, IsDBA string }
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
	if cfg.StrictNetworkEncryption && cfg.Expected.Version != "" {
		t.Fatal("strict network profile uses ExtendedVersion and Expected.VersionFull for optional version verification")
	}
	if cfg.Expected.VersionFull != "" && !cfg.ExtendedVersion {
		t.Fatal("Expected.VersionFull requires ExtendedVersion")
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
	type identity struct {
		User, Database, Container, Version, VersionFull, IsDBA, Selected string
		NetworkServiceBanners                                            []string `json:",omitempty"`
	}
	observe := func(lease *sql.Conn) (identity, error) {
		var result identity
		if cfg.StrictNetworkEncryption {
			if err := lease.PingContext(ctx); err != nil {
				return result, fmt.Errorf("strict network ping: %w", err)
			}
			banners, err := nativeNetworkBanners(ctx, lease)
			if err != nil {
				return result, fmt.Errorf("current-session network banners: %w", err)
			}
			result.NetworkServiceBanners = banners
		}
		var err error
		if cfg.StrictNetworkEncryption {
			// The network regression can use a PDB login without access to
			// V$DATABASE or V$INSTANCE. Qualify the fixture release separately
			// if that login cannot select the optional version projection.
			err = lease.QueryRowContext(ctx, "SELECT USER,SYS_CONTEXT('USERENV','DB_NAME'),SYS_CONTEXT('USERENV','ISDBA') FROM DUAL").Scan(&result.User, &result.Database, &result.IsDBA)
		} else {
			err = lease.QueryRowContext(ctx, "SELECT USER,(SELECT NAME FROM V$DATABASE),VERSION,SYS_CONTEXT('USERENV','ISDBA') FROM V$INSTANCE").Scan(&result.User, &result.Database, &result.Version, &result.IsDBA)
		}
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
		secrets := []string{cfg.DSN}
		if parsed, parseErr := url.Parse(cfg.DSN); parseErr == nil && parsed.User != nil {
			secrets = append(secrets, parsed.User.Username())
			if password, ok := parsed.User.Password(); ok {
				secrets = append(secrets, password)
			}
		}
		messages, diagnosticErr := diagnostic.Connection(fmt.Errorf("%s", err.Error()), secrets...)
		if diagnosticErr != nil {
			t.Fatalf("%s failed: type=%T ORA=%d; diagnostic capacity exceeded", phase, err, code)
		}
		t.Fatalf("%s failed: type=%T ORA=%d causes=%+v", phase, err, code, messages)
	}
	wantMode, wantDials := cfg.Mode, int32(4)
	if cfg.Mode == compat.Automatic {
		wantMode = compat.V2
		if cfg.StrictNetworkEncryption {
			wantMode, wantDials = compat.V3, 8
		}
	}
	var observations []identity
	checkIdentity := func(value identity) bool {
		expected := cfg.Expected
		return (expected.User == "" || value.User == expected.User) && (expected.Database == "" || value.Database == expected.Database) && (expected.Container == "" || value.Container == expected.Container) && (expected.Version == "" || value.Version == expected.Version) && (expected.VersionFull == "" || value.VersionFull == expected.VersionFull) && (expected.IsDBA == "" || value.IsDBA == expected.IsDBA) && value.Selected == wantMode.String()
	}
	proof := map[string]any{
		"checkedUTC": time.Now().UTC(), "sourceRevision": cfg.SourceRevision,
		"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"mode": cfg.Mode.String(), "patchInventory": "Not queried",
		"scope": "Four concurrent physical admissions, identity, repeated prepared OUT, exact scalar/NULL/Unicode and temporary CLOB only",
	}
	writeProof := func() {
		t.Helper()
		proof["identities"] = observations
		proof["physicalDials"] = dialer.calls.Load()
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg.ProofPath, data, 0600); err != nil {
			t.Fatal("cannot retain native proof")
		}
	}
	if cfg.StrictNetworkEncryption {
		proof["scope"] = "Four concurrent physical admissions, identity, ping and current-session native encryption and integrity banners"
		proof["strictNetworkEncryption"] = true
		proof["completed"] = false
		proof["stage"] = "admission"
		// Replace any previous success receipt before attempting a new admission.
		writeProof()
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
	defer func() {
		for _, lease := range leases {
			if lease != nil {
				lease.Close()
			}
		}
	}()
	acquired := len(leases) - len(failures)
	if cfg.StrictNetworkEncryption {
		proof["successfulAdmissions"] = acquired
		if acquired == len(leases) {
			proof["stage"] = "session observation"
		}
		writeProof()
	}
	if acquired != len(leases) {
		t.Logf("concurrent physical admissions: %d/%d leases acquired; %d socket dials", acquired, len(leases), dialer.calls.Load())
		safeFailure("concurrent admission", <-failures)
	}
	for _, lease := range leases {
		value, err := observe(lease)
		observations = append(observations, value)
		if err != nil {
			if cfg.StrictNetworkEncryption {
				writeProof()
			}
			safeFailure("session observation", err)
		}
	}
	// Retain observations even when caller-supplied expectations fail.
	if cfg.StrictNetworkEncryption {
		proof["stage"] = "session assertions"
	}
	writeProof()
	for _, value := range observations {
		if !checkIdentity(value) {
			t.Fatal("identity differs from supplied expectation; observed proof retained")
		}
		if cfg.StrictNetworkEncryption && !hasStrictNetworkAdapters(value.NetworkServiceBanners) {
			t.Fatal("Oracle did not report both AES256 encryption and SHA256 checksumming; observed proof retained")
		}
	}
	if got := dialer.calls.Load(); got != wantDials {
		t.Fatalf("physical dials = %d, want %d", got, wantDials)
	}
	if cfg.StrictNetworkEncryption {
		proof["oracleConfirmedAES256AndSHA256Sessions"] = len(observations)
		proof["completed"] = true
		proof["stage"] = "complete"
		writeProof()
		t.Log("Oracle reported AES256 encryption and SHA256 checksumming on all four physical sessions")
		return
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
	exactNumber, err := compat.NewNumber("9007199254740993")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.QueryRowContext(ctx, "SELECT 9007199254740993 FROM DUAL").Scan(&number); err != nil {
		safeFailure("native NUMBER literal", err)
	}
	if number != "9007199254740993" {
		t.Fatal("native NUMBER literal rounded")
	}
	if err := lease.QueryRowContext(ctx, "SELECT :1 FROM DUAL", exactNumber).Scan(&number); err != nil {
		safeFailure("native NUMBER bind", err)
	}
	if number != "9007199254740993" {
		t.Fatal("native NUMBER bind rounded")
	}
	outCharacters := cfg.OutCharacters
	if outCharacters == 0 {
		outCharacters = 32767
	}
	proof["declaredOutputCharacters"] = outCharacters
	statement, err := lease.PrepareContext(ctx, "BEGIN :1 := :2; :3 := TO_CHAR(9007199254740993); :4 := NULL; END;")
	if err != nil {
		safeFailure("prepare OUT", err)
	}
	for i := range 3 {
		text, number = "", ""
		null = sql.NullString{String: "previous", Valid: true}
		input := fmt.Sprintf("é漢-%d", i)
		if _, err := statement.ExecContext(ctx, compat.Out{Dest: &text, Size: outCharacters}, input, compat.Out{Dest: &number, Size: 64}, compat.Out{Dest: &null, Size: 64}); err != nil {
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
	if text, ok := value.(string); ok && text != strings.Repeat(input, 25) {
		proof["clobObservedBytes"] = len(text)
		proof["clobObservedRunes"] = utf8.RuneCountInString(text)
		proof["clobObservedSHA256"] = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		proof["clobExpectedBytes"] = len(strings.Repeat(input, 25))
		proof["clobPrefixHex"] = fmt.Sprintf("%x", []byte(text)[:min(32, len(text))])
		writeProof()
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
	proof["nativeIntegerNumber"] = "9007199254740993 literal and typed bind"
	proof["preparedOUTExecutions"] = 3
	proof["temporaryClobBytes"] = len(strings.Repeat(input, 25))
	proof["completed"] = true
	writeProof()
	t.Log("bounded native compatibility checks passed; scope and untested conditions retained")
}

func nativeNetworkBanners(ctx context.Context, lease *sql.Conn) ([]string, error) {
	// Read on the held lease and constrain the report to its session. On
	// eligible fixtures USERENV supplies SID without requiring V$MYSTAT access.
	rows, err := lease.QueryContext(ctx, "SELECT NETWORK_SERVICE_BANNER FROM V$SESSION_CONNECT_INFO WHERE SID = TO_NUMBER(SYS_CONTEXT('USERENV','SID'))")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var banners []string
	for rows.Next() {
		var banner string
		if err := rows.Scan(&banner); err != nil {
			return nil, err
		}
		banners = append(banners, banner)
	}
	return banners, rows.Err()
}

func hasStrictNetworkAdapters(banners []string) bool {
	var encryption, integrity bool
	for _, banner := range banners {
		encryption = encryption || strings.HasPrefix(banner, "AES256 Encryption service adapter ")
		integrity = integrity || strings.HasPrefix(banner, "SHA256 Crypto-checksumming service adapter ")
	}
	return encryption && integrity
}

func TestStrictNetworkAdapters(t *testing.T) {
	const aes256 = "AES256 Encryption service adapter for Linux: Version 23.26.4.0.0 - Production"
	const sha256 = "SHA256 Crypto-checksumming service adapter for Linux: Version 23.26.4.0.0 - Production"
	for _, tc := range []struct {
		name    string
		banners []string
		want    bool
	}{
		{name: "both exact algorithms", banners: []string{sha256, aes256}, want: true},
		{name: "no banners"},
		{name: "encryption only", banners: []string{aes256, "Crypto-checksumming service for Linux"}},
		{name: "checksum only", banners: []string{"Encryption service for Linux", sha256}},
		{name: "different algorithms", banners: []string{"AES128 Encryption service adapter for Linux", "SHA1 Crypto-checksumming service adapter for Linux"}},
		{name: "unrelated banner", banners: []string{"TCP/IP NT Protocol Adapter - " + aes256, sha256}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasStrictNetworkAdapters(tc.banners); got != tc.want {
				t.Fatalf("hasStrictNetworkAdapters = %t, want %t", got, tc.want)
			}
		})
	}
}
