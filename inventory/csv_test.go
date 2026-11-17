package inventory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRejectsCaseInsensitiveDuplicateNamesAtSourceLines(t *testing.T) {
	t.Parallel()
	for _, names := range [][2]string{{"Db1", "Db1"}, {"Db1", "db1"}, {" Db1 ", " DB1 "}, {"Σ", "ς"}} {
		t.Run(names[0]+"/"+names[1], func(t *testing.T) {
			data := "# inventory\n\npassword,name,username,connect\n\"private\npassword\"," + names[0] + ",secret-user,secret-host\n# ignored\n\nprivate-secret," + names[1] + ",secret-user,secret-host\n"
			entries, err := Parse([]byte(data))
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Code != DuplicateName {
				t.Fatalf("Parse() error = %v, want duplicate-name validation", err)
			}
			if entries != nil {
				t.Fatal("invalid inventory returned collectable entries")
			}
			if validation.Row != 8 || validation.FirstRow != 4 {
				t.Errorf("source lines = %d, %d; want 8, 4", validation.Row, validation.FirstRow)
			}
			for _, want := range []string{"8", "4", strings.TrimSpace(names[1])} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("diagnostic %q omits %q", err.Error(), want)
				}
			}
			for _, secret := range []string{"private", "secret-user", "secret-host"} {
				if strings.Contains(err.Error(), secret) {
					t.Error("diagnostic exposes a credential or connection value")
				}
			}
		})
	}
}

func TestParsePreservesPasswordAndAcceptsEasyConnect(t *testing.T) {
	t.Parallel()

	data := []byte("\uFEFFname,connect,username,password\r\n" +
		" db1 ,oracle://db.example:1521/service, app ,\"  p,a\"\"ss  \"")
	entries, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.Name != "db1" || got.Connect != "oracle://db.example:1521/service" || got.Username != "app" {
		t.Fatalf("non-secret fields were not normalized: %#v", got)
	}
	if got.Password != `  p,a"ss  ` {
		t.Fatalf("Password = %q; whitespace or CSV quoting was not preserved", got.Password)
	}
}

func TestParseRejectsCollidingEffectiveTargetLabels(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, first, second, label string }{
		{"tab", "CDB", "CD\tB", "CDB"},
		{"ANSI", "CDB", "\x1b[31mCDB\x1b[0m", "CDB"},
		{"case after sanitizing", "CDB", "cd\tb", "cdb"},
		{"long ASCII", strings.Repeat("a", 80) + "x", strings.Repeat("a", 80) + "y", strings.Repeat("a", 80)},
		{"long Unicode", strings.Repeat("é", 80) + "x", strings.Repeat("é", 80) + "y", strings.Repeat("é", 80)},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := "# inventory\nname,connect,username,password\n" + test.first + ",private-host,private-user,private-password\n\n" + test.second + ",private-host,private-user,private-password\n"
			entries, err := Parse([]byte(data))
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Code != DuplicateName {
				t.Fatalf("Parse() error = %v, want duplicate-name validation", err)
			}
			if entries != nil || validation.FirstRow != 3 || validation.Row != 5 || validation.Target != test.label {
				t.Fatalf("invalid collision result: entries=%d, error=%+v", len(entries), validation)
			}
			if strings.Contains(err.Error(), "private-") || strings.ContainsAny(err.Error(), "\x1b\t") {
				t.Fatal("collision diagnostic leaked a credential or terminal control")
			}
		})
	}
}

func TestParsePreservesDistinctEffectiveTargetLabels(t *testing.T) {
	t.Parallel()
	first := strings.Repeat("a", 79) + "x-long"
	second := strings.Repeat("a", 79) + "y-long"
	entries, err := Parse([]byte("name,connect,username,password\n" + first + ",TNS,app,pw\n" + second + ",TNS,app,pw\n"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("distinct labels rejected: %v", err)
	}
	if entries[0].Name != first || entries[1].Name != second {
		t.Fatal("inventory names were replaced with their display labels")
	}
}

func TestParseRejectsNameWithNoVisibleContent(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("name,connect,username,password\n\x1b[31m\x1b[0m,TNS,app,pw\n"))
	var validation *ValidationError
	if !errors.As(err, &validation) || validation.Code != RowEmptyField || validation.Field != "name" {
		t.Fatalf("Parse() error = %v, want empty-name validation", err)
	}
}

func TestParseIgnoresCommentedLines(t *testing.T) {
	t.Parallel()

	data := []byte("# local database inventory\n" +
		"name,connect,username,password\n" +
		"active,TNS_ACTIVE,app,active-password\n" +
		"#disabled,TNS_DISABLED,app,disabled-password\n")
	entries, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if got := entries[0].Name; got != "active" {
		t.Fatalf("entries[0].Name = %q, want %q", got, "active")
	}
}

func TestParseRejectsUnsafeOrInvalidInventory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		csv  string
		want string
	}{
		{name: "unknown header", csv: "name,connect,username,extra\ndb1,TNS,app,pw", want: "unknown"},
		{name: "duplicate name", csv: "name,connect,username,password\ndb1,TNS,app,pw\ndb1,TNS2,app,pw", want: "duplicates name"},
		{name: "URL credentials", csv: "name,connect,username,password\ndb1,oracle://scott:tiger@db/svc,app,pw", want: "embeds credentials"},
		{name: "URL username query", csv: "name,connect,username,password\ndb1,oracle://db/svc?username=scott,app,pw", want: "embeds credentials"},
		{name: "URL uppercase user query", csv: "name,connect,username,password\ndb1,mysql://db/app?USER=scott,app,pw", want: "embeds credentials"},
		{name: "URL UID query", csv: "name,connect,username,password\ndb1,sqlserver://db/app?uid=scott,app,pw", want: "embeds credentials"},
		{name: "URL password query", csv: "name,connect,username,password\ndb1,mysql://db/app?password=tiger,app,pw", want: "embeds credentials"},
		{name: "URL abbreviated password query", csv: "name,connect,username,password\ndb1,sqlserver://db/app?PwD=tiger,app,pw", want: "embeds credentials"},
		{name: "legacy credentials", csv: "name,connect,username,password\ndb1,scott/tiger@db,app,pw", want: "embeds credentials"},
		{name: "empty password", csv: "name,connect,username,password\ndb1,TNS,app,", want: "empty password"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(test.csv))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse() error = %v, want substring %q", err, test.want)
			}
			if strings.Contains(err.Error(), "tiger") || strings.Contains(err.Error(), "pw") {
				t.Fatalf("Parse() leaked a credential: %v", err)
			}
		})
	}
}

func TestParseAllowsArbitraryNonCredentialURIParameters(t *testing.T) {
	t.Parallel()

	const connect = "mysql://db.example/app?tls=custom&future-option=preserved&multi=one&multi=two"
	entries, err := Parse([]byte("name,connect,username,password\nmysql-db," + connect + ",app,pw"))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got := entries[0].Connect; got != connect {
		t.Fatalf("Connect = %q, want %q", got, connect)
	}
}

func TestParseNormalizesDiagnosticTargetLabel(t *testing.T) {
	t.Parallel()

	data := []byte("name,connect,username,password\n\x1b[31mdb\x1b[0m,,app,pw\n")
	_, err := Parse(data)
	if err == nil {
		t.Fatal("Parse() error = nil, want empty-connect error")
	}
	if got, want := err.Error(), "inventory row 2 (db) has an empty connect value"; got != want {
		t.Fatalf("Parse() error = %q, want %q", got, want)
	}
}

func TestParseUsesTheFileByteLimit(t *testing.T) {
	_, err := Parse(make([]byte, (10<<20)+1))
	failure, ok := err.(*ValidationError)
	if !ok || failure.Code != FileTooLarge {
		t.Fatalf("unbounded byte admission: %v", err)
	}
}

func TestParseRowCapacityStopsAtFirstOverflow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rows     int
		tail     string
		overflow bool
	}{
		{"exact", maxRows, "", false},
		{"overflow", maxRows + 1, "", true},
		{"malformed first extra record", maxRows, "\"private malformed", false},
		{"unadmitted malformed tail", maxRows + 1, "\"private malformed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var data strings.Builder
			data.WriteString("name,connect,username,password\n")
			for i := range tc.rows {
				fmt.Fprintf(&data, "db%d,TNS,app,  private-password  \n", i)
			}
			data.WriteString(tc.tail)
			entries, err := Parse([]byte(data.String()))
			if tc.tail != "" && !tc.overflow {
				var failure *ValidationError
				if entries != nil || !errors.As(err, &failure) || failure.Code != CSVInvalid {
					t.Fatal("malformed first extra record lost syntax failure", err)
				}
			} else if tc.overflow {
				var failure *ValidationError
				if entries != nil || !errors.As(err, &failure) || failure.Code != TooManyRows || failure.Limit != maxRows {
					t.Fatalf("row admission did not stop at capacity: entries=%d error=%v", len(entries), err)
				}
				if strings.Contains(err.Error(), "private") {
					t.Fatal("overflow exposed an unadmitted value")
				}
			} else if err != nil || len(entries) != maxRows || entries[maxRows-1].Name != "db9999" || entries[maxRows-1].Password != "  private-password  " {
				t.Fatal("exact row boundary or credential bytes changed", err)
			}
		})
	}
}

func TestReadFilePreservesValuesAndRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private-inventory.csv")
	const input = "name,connect,username,password\nsample,private-host,private-user,\"  private,password  \"\n"
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadFile(path)
	if err != nil || len(entries) != 1 || entries[0].Connect != "private-host" || entries[0].Username != "private-user" || entries[0].Password != "  private,password  " {
		t.Fatal("file admission changed parsed values", err)
	}
	for _, tc := range []struct {
		name, path string
		code       ValidationCode
	}{
		{"missing", filepath.Join(root, "private-missing.csv"), FileUnreadable},
		{"directory", root, FileUnsafe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := ReadFile(tc.path)
			var failure *ValidationError
			if entries != nil || !errors.As(err, &failure) || failure.Code != tc.code || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), root) {
				t.Fatal("file failure lost its typed/redacted contract", err)
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(root, "link.csv")
		if err := os.Symlink(path, link); err != nil {
			t.Skip("symbolic links unavailable on this host")
		}
		entries, err := ReadFile(link)
		var failure *ValidationError
		if entries != nil || !errors.As(err, &failure) || failure.Code != FileUnsafe {
			t.Fatal("symbolic link admitted", err)
		}
	})
}

func TestReadFileByteBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.csv")
	const prefix = "name,connect,username,password\nsample,TNS,app,password\n#"
	data := prefix + strings.Repeat(" ", maxFileSize-len(prefix)-1) + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadFile(path)
	if err != nil || len(entries) != 1 || entries[0].Name != "sample" || entries[0].Password != "password" {
		t.Fatal("exact byte boundary rejected or changed", err)
	}
	if err := os.Truncate(path, maxFileSize+1); err != nil {
		t.Fatal(err)
	}
	entries, err = ReadFile(path)
	var failure *ValidationError
	if entries != nil || !errors.As(err, &failure) || failure.Code != FileTooLarge || failure.Limit != maxFileSize {
		t.Fatal("file byte overflow did not reject before parsing", err)
	}
}

func TestURLValidationDelegatesThroughParseAndReadFile(t *testing.T) {
	const connect = "custom://db.example/service?future-mode=enabled"
	input := []byte("name,connect,username,password\nsample," + connect + ",private-user,private-password\n")
	path := filepath.Join(t.TempDir(), "inventory.csv")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	for _, fromFile := range []bool{false, true} {
		for _, reject := range []bool{false, true} {
			t.Run(fmt.Sprintf("file=%t/reject=%t", fromFile, reject), func(t *testing.T) {
				calls := 0
				options := Options{ValidateURL: func(value string) error {
					calls++
					if value != connect {
						t.Error("URL callback received a changed connection value")
					}
					if reject {
						return errors.New("private engine failure")
					}
					return nil
				}}
				var entries []Entry
				var err error
				if fromFile {
					entries, err = ReadFile(path, options)
				} else {
					entries, err = Parse(input, options)
				}
				if calls != 1 {
					t.Fatal("URL callback was skipped or repeated", calls)
				}
				if reject {
					var failure *ValidationError
					if entries != nil || !errors.As(err, &failure) || failure.Code != EmbeddedCredentials || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), connect) {
						t.Fatal("callback failure lost redacted admission semantics", err)
					}
				} else if err != nil || len(entries) != 1 || entries[0].Connect != connect || entries[0].Password != "private-password" {
					t.Fatal("callback success changed admitted values", err)
				}
			})
		}
	}
}
