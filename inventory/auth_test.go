package inventory

import (
	"bytes"
	"encoding/csv"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Positional literals pin the published struct layouts as well as signatures.
var _ = Entry{"", "", "", "", "", ""}
var _ = Options{nil}

func authCSV(t *testing.T, rows ...[]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"name", "connect", "username", "password", "auth", "wallet_directory", "wallet_credential"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestParseAuthenticatedPreservesMixedRows(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "nonexistent", "$UNEXPANDED")
	key := "  MiXeD, Key/雪  "
	password := "  p,a\"ss  "
	input := authCSV(t,
		[]string{"legacy", "TNS_A", " app ", password, "", "", ""},
		[]string{"explicit", "TNS_B", " app ", password, " PaSsWoRd ", "", ""},
		[]string{"wallet", "TNS_C", "", "", " WaLlEt ", directory, key})
	got, err := ParseAuthenticated(input)
	want := []AuthEntry{
		{Entry: Entry{Name: "legacy", Connect: "TNS_A", Username: "app", Password: password}, Auth: "password"},
		{Entry: Entry{Name: "explicit", Connect: "TNS_B", Username: "app", Password: password}, Auth: "password"},
		{Entry: Entry{Name: "wallet", Connect: "TNS_C"}, Auth: "wallet", Wallet: &WalletReference{Directory: directory, Credential: key}},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("mixed admission differs: got %#v, %v", got, err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wallet directory must remain absent", err)
	}
	_, err = Parse(input)
	var failure *ValidationError
	if !errors.As(err, &failure) || failure.Code != HeaderUnknown || failure.Index != 5 {
		t.Fatalf("legacy headers changed: %v", err)
	}
}

func TestParseAuthenticatedPreservesLegacyAdmission(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		"name,connect,username,password\n db,TNS, app ,\" p,a\"\"ss \"\n",
		"name,connect,username,password\ndb,TNS,app,\n",
		"name,connect,username,password\ndb,TNS,app,pw\nDB,TNS,app,pw\n",
		"name,connect,username,password\ndb,oracle://user:private@host/service,app,pw\n",
		"name,connect,username,password\ndb,TNS,app,pw,extra\n",
	} {
		legacy, oldErr := Parse([]byte(input))
		got, err := ParseAuthenticated([]byte(input))
		if oldErr != nil {
			var oldFailure, failure *ValidationError
			if err == nil || !errors.As(oldErr, &oldFailure) || !errors.As(err, &failure) || !reflect.DeepEqual(oldFailure, failure) || oldErr.Error() != err.Error() || got != nil {
				t.Fatalf("legacy error/result differs: %v / %v", oldErr, err)
			}
			continue
		}
		if err != nil || len(got) != len(legacy) {
			t.Fatal("legacy rows changed", err)
		}
		for i := range legacy {
			if got[i].Entry != legacy[i] || got[i].Auth != "password" || got[i].Wallet != nil {
				t.Fatal("legacy values changed")
			}
		}
	}
}

func TestParseAuthenticatedRejectsUnsafeReferences(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "private-directory")
	for _, tc := range []struct {
		name   string
		column int
		value  string
		code   ValidationCode
		field  string
	}{
		{"unknown auth", 4, "private-unknown-auth", RowFieldInvalid, "auth"},
		{"raw user whitespace", 2, " ", RowFieldConflict, "username"},
		{"raw user", 2, "private-user", RowFieldConflict, "username"},
		{"raw password whitespace", 3, " ", RowFieldConflict, "password"},
		{"missing directory", 5, "", RowEmptyField, "wallet_directory"},
		{"relative directory", 5, "private-relative", RowFieldInvalid, "wallet_directory"},
		{"directory control", 5, directory + "\nprivate-directory", RowFieldInvalid, "wallet_directory"},
		{"directory invalid UTF8", 5, directory + "\xff", RowFieldInvalid, "wallet_directory"},
		{"missing key", 6, "", RowEmptyField, "wallet_credential"},
		{"blank key", 6, "   ", RowEmptyField, "wallet_credential"},
		{"key NUL", 6, "private-key\x00", RowFieldInvalid, "wallet_credential"},
		{"key DEL", 6, "private-key\x7f", RowFieldInvalid, "wallet_credential"},
		{"key Unicode control", 6, "private-key\u0085", RowFieldInvalid, "wallet_credential"},
		{"key invalid UTF8", 6, "private-key\xff", RowFieldInvalid, "wallet_credential"},
		{"key byte overflow", 6, strings.Repeat("é", 2048) + "x", RowFieldTooLong, "wallet_credential"},
		{"directory byte overflow", 5, directory + strings.Repeat("x", 4097-len(directory)), RowFieldTooLong, "wallet_directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			row := []string{"db", "private-host", "", "", "wallet", directory, "private-key"}
			row[tc.column] = tc.value
			entries, err := ParseAuthenticated(authCSV(t, row))
			var failure *ValidationError
			if entries != nil || !errors.As(err, &failure) || failure.Code != tc.code || failure.Field != tc.field || failure.Row != 2 {
				t.Fatalf("unsafe reference admission: entries=%d err=%+v", len(entries), failure)
			}
			if strings.Contains(err.Error(), "private-") || strings.ContainsAny(err.Error(), "\x00\n\x7f\u0085") {
				t.Fatal("diagnostic exposed reference data", err)
			}
		})
	}
}

func TestParseAuthenticatedRejectsPasswordReferenceConflicts(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"", "password"} {
		for _, column := range []int{5, 6} {
			row := []string{"db", "TNS", "app", " pw ", mode, "", ""}
			row[column] = "private-reference"
			entries, err := ParseAuthenticated(authCSV(t, row))
			var failure *ValidationError
			if entries != nil || !errors.As(err, &failure) || failure.Code != RowFieldConflict || strings.Contains(err.Error(), "private-reference") {
				t.Fatalf("password reference conflict accepted or leaked: %v", err)
			}
		}
	}
}

func TestParseAuthenticatedReferenceByteAndPlatformBounds(t *testing.T) {
	t.Parallel()
	prefix := filepath.Join(t.TempDir(), "")
	directory := prefix + strings.Repeat("x", 4096-len(prefix))
	key := strings.Repeat("é", 2048)
	entries, err := ParseAuthenticated(authCSV(t, []string{"db", "TNS", "", "", "wallet", directory, key}))
	if err != nil || len(entries) != 1 || entries[0].Wallet.Directory != directory || entries[0].Wallet.Credential != key {
		t.Fatal("exact reference byte boundary rejected", err)
	}
	for _, tc := range []struct {
		directory string
		valid     bool
	}{
		{"relative", false}, {`C:relative`, false}, {`\root-relative`, false},
		{`C:\wallet`, runtime.GOOS == "windows"},
		{`\\server\share\wallet`, runtime.GOOS == "windows"},
		{"/wallet", runtime.GOOS != "windows"},
	} {
		got, err := ParseAuthenticated(authCSV(t, []string{"db", "TNS", "", "", "wallet", tc.directory, "Key"}))
		if (err == nil) != tc.valid || (err != nil && got != nil) {
			t.Fatalf("platform path %q: %v", tc.directory, err)
		}
	}
}

func TestReadFileAuthenticatedSharesAdmissionAndURLPolicy(t *testing.T) {
	t.Parallel()
	const connect = "custom://db.example/service?future=allowed"
	directory := filepath.Join(t.TempDir(), "absent-wallet")
	input := authCSV(t, []string{"db", connect, "", "", "wallet", directory, " Key "})
	file := filepath.Join(t.TempDir(), "inventory.csv")
	if err := os.WriteFile(file, input, 0600); err != nil {
		t.Fatal(err)
	}
	for _, fromFile := range []bool{false, true} {
		for _, reject := range []bool{false, true} {
			calls := 0
			options := Options{ValidateURL: func(value string) error {
				calls++
				if value != connect {
					t.Error("URL changed")
				}
				if reject {
					return errors.New("private-callback")
				}
				return nil
			}}
			var got []AuthEntry
			var err error
			if fromFile {
				got, err = ReadFileAuthenticated(file, options)
			} else {
				got, err = ParseAuthenticated(input, options)
			}
			if calls != 1 {
				t.Fatal("URL policy skipped/repeated", calls)
			}
			if reject {
				var failure *ValidationError
				if got != nil || !errors.As(err, &failure) || failure.Code != EmbeddedCredentials || strings.Contains(err.Error(), "private-callback") {
					t.Fatal("URL failure changed or leaked", err)
				}
			} else if err != nil || len(got) != 1 || got[0].Wallet.Credential != " Key " {
				t.Fatal("reference changed", err)
			}
		}
	}
	for _, path := range []string{filepath.Dir(file), file + "-missing"} {
		got, err := ReadFileAuthenticated(path)
		var failure *ValidationError
		if got != nil || !errors.As(err, &failure) || (failure.Code != FileUnsafe && failure.Code != FileUnreadable) {
			t.Fatal("file admission bypassed", err)
		}
	}
}

func TestParseAuthenticatedSharesInventoryLimits(t *testing.T) {
	t.Parallel()
	for _, input := range [][]byte{
		bytes.Repeat([]byte("x"), maxFileSize+1),
		[]byte("name,connect,username,password,auth,unknown\ndb,TNS,app,pw,password,x\n"),
		[]byte("name,connect,username,password,auth,auth\ndb,TNS,app,pw,password,password\n"),
		[]byte("name,connect,username,auth\ndb,TNS,app,wallet\n"),
	} {
		got, err := ParseAuthenticated(input)
		if err == nil || got != nil {
			t.Fatal("invalid header/size returned rows")
		}
	}
}

func TestParseAuthenticatedDuplicateAcrossModesKeepsSourceLines(t *testing.T) {
	t.Parallel()
	directory := filepath.Join(t.TempDir(), "absent")
	input := "# inventory\nname,connect,username,password,auth,wallet_directory,wallet_credential\n" +
		"Db,TNS,app,private-password,password,,\n# separator\n" +
		"db,TNS,,,wallet," + directory + ",private-key\n"
	got, err := ParseAuthenticated([]byte(input))
	var failure *ValidationError
	if got != nil || !errors.As(err, &failure) || failure.Code != DuplicateName || failure.Row != 5 || failure.FirstRow != 3 {
		t.Fatalf("mixed duplicate lost atomic/source-line contract: %v", err)
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatal("duplicate diagnostic leaked authentication data")
	}
}
