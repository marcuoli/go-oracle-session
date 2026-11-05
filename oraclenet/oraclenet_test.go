package oraclenet_test

import (
	"errors"
	"github.com/marcuoli/go-oracle-session/oraclenet"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func descriptor(wallet string) string {
	return `(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=db.example.invalid)(PORT=1522))(CONNECT_DATA=(SERVICE_NAME=service))(SECURITY=(MY_WALLET_DIRECTORY="` + wallet + `")))`
}
func TestAliasLookupPreservesConnectionCompatibility(t *testing.T) {
	root := t.TempDir()
	first := descriptor(`wallet, ação`)
	last := descriptor(`last`)
	write(t, filepath.Join(root, "tnsnames.ora"), "# comment\nFIRST, SECOND = "+first+" # ignored\nFIRST = "+last+"\n")
	for _, test := range []struct{ alias, want string }{{"first", last}, {"SeCoNd", first}} {
		got, err := oraclenet.ResolveAlias(test.alias, root)
		if err != nil || got != test.want {
			t.Fatal(got, err)
		}
	}
	if err := oraclenet.ValidateTNSFiles("SECOND", root); err != nil {
		t.Fatal(err)
	}
	if !oraclenet.IsAlias("db_high.example") || oraclenet.IsAlias("user/password@host") || !oraclenet.IsEasyConnect("host/service") {
		t.Fatal("endpoint classification changed")
	}
}
func TestAliasFailuresRemainTypedAndDoNotExposePrivatePaths(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name, alias, directory, contents string
		reason                           oraclenet.ResolutionReason
	}{
		{"unconfigured", "DB", "", "", oraclenet.TNSAdminNotConfigured},
		{"invalid", "bad/password", root, "", oraclenet.TNSInvalidAlias},
		{"missing file", "DB", root, "", oraclenet.TNSFileUnavailable},
		{"unknown", "MISSING", root, "DB=(DESCRIPTION=(ADDRESS=(HOST=host)))", oraclenet.TNSAliasNotFound},
		{"malformed", "DB", root, "DB=(DESCRIPTION=(ADDRESS=(HOST=host))", oraclenet.TNSFileMalformed},
		{"large", "DB", root, strings.Repeat("x", (1<<20)+1), oraclenet.TNSFileTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			file := filepath.Join(root, "tnsnames.ora")
			_ = os.Remove(file)
			if test.contents != "" {
				write(t, file, test.contents)
			}
			_, err := oraclenet.ResolveAlias(test.alias, test.directory)
			var typed *oraclenet.ResolutionError
			if !errors.As(err, &typed) || typed.Reason != test.reason || !errors.Is(err, oraclenet.ErrTNSAliasUnavailable) {
				t.Fatal(err)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "password") {
				t.Fatal("typed error formatted private data")
			}
		})
	}
}
func TestWalletDescriptorPrecedenceAndQuotedValues(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "sqlnet.ora"), `WALLET_LOCATION=(SOURCE=(METHOD=file)(METHOD_DATA=(DIRECTORY="sql wallet")))`)
	got, err := oraclenet.ResolveWallet(descriptor("descriptor, ação"), root)
	if err != nil || got != filepath.Join(root, "descriptor, ação") {
		t.Fatal(got, err)
	}
	quoted := `(DESCRIPTION=(SECURITY=(SSL_SERVER_CERT_DN="CN=(MY_WALLET_DIRECTORY=decoy)#x")(MY_WALLET_DIRECTORY="real (#), ação")))`
	got, err = oraclenet.ResolveWallet(quoted, root)
	if err != nil || got != filepath.Join(root, "real (#), ação") {
		t.Fatal(got, err)
	}
	conflict := `(DESCRIPTION_LIST=` + descriptor("one") + descriptor("two") + `)`
	if _, err := oraclenet.ResolveWallet(conflict, root); err == nil {
		t.Fatal("conflicting descriptor wallets accepted")
	}
	got, err = oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root)
	if err != nil || got != filepath.Join(root, "sql wallet") {
		t.Fatal(got, err)
	}
}
func TestWalletFallbackDoesNotOverrideExplicitInvalidConfiguration(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cwallet.sso"), "not a wallet; contents must never be loaded here")
	got, err := oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root)
	if err != nil || got != root {
		t.Fatal(got, err)
	}
	for _, content := range []string{`WALLET_LOCATION=(SOURCE=(METHOD=ldap)(METHOD_DATA=(DIRECTORY=elsewhere)))`, `IFILE=other.ora`, `WALLET_LOCATION=(SOURCE=(METHOD=file))`, `WALLET_LOCATION=(SOURCE=(METHOD=file)(METHOD_DATA=(DIRECTORY=one)(DIRECTORY=two)))`} {
		write(t, filepath.Join(root, "sqlnet.ora"), content)
		if _, err := oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root); err == nil {
			t.Fatal("invalid explicit configuration fell back")
		}
	}
	write(t, filepath.Join(root, "sqlnet.ora"), strings.Repeat("x", (1<<20)+1))
	if _, err := oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root); err == nil {
		t.Fatal("oversized sqlnet.ora accepted")
	}
}
func TestWalletEnvironmentExpansionAndLiteralURLPolicy(t *testing.T) {
	root := filepath.Join(t.TempDir(), "literal, ação $UNEXPANDED")
	t.Setenv("NET_TEST_ROOT", root)
	raw := `oracle://db.example.invalid:1522/service?SSL=enable&SSL%20Verify=false&wallet=%24NET_TEST_ROOT&wallet=second&wallet+password=synthetic&other=a%2cb`
	parsed, _ := url.Parse(raw)
	if err := oraclenet.PrepareEasyConnectWallet(parsed, ""); err != nil {
		t.Fatal(err)
	}
	expected := strings.Replace(raw, "wallet=%24NET_TEST_ROOT", "wallet="+url.QueryEscape(root), 1)
	if parsed.String() != expected {
		t.Fatalf("unrelated URL text changed: %s", parsed)
	}
	t.Setenv("TNS_ADMIN", "wrong")
	got, err := oraclenet.ResolveWallet(descriptor("${TNS_ADMIN}"), root)
	if err != nil || got != root {
		t.Fatal(got, err)
	}
	got, err = oraclenet.ResolveWallet(descriptor("?/network/admin"), root)
	if err != nil || got != root {
		t.Fatal(got, err)
	}
	t.Setenv("NET_MISSING", "")
	parsed, _ = url.Parse("oracle://host/service?wallet=%24NET_MISSING")
	if err := oraclenet.PrepareEasyConnectWallet(parsed, ""); err == nil {
		t.Fatal("missing environment path accepted")
	}
	if err := oraclenet.PrepareEasyConnectWallet(nil, ""); !errors.Is(err, oraclenet.ErrInvalidEasyConnect) {
		t.Fatal(err)
	}
}
func TestDescriptorOnlyResolutionDoesNotDiscoverWorkingDirectoryWallet(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cwallet.sso"), "synthetic")
	t.Chdir(root)
	got, found, err := oraclenet.ResolveDescriptorWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", "")
	if err != nil || found || got != "" {
		t.Fatal(got, found, err)
	}
	got, found, err = oraclenet.ResolveDescriptorWallet(descriptor("relative"), "")
	if err != nil || !found || got != filepath.Join(root, "relative") {
		t.Fatal(got, found, err)
	}
}
func TestWalletNativePathBoundary(t *testing.T) {
	parsed, _ := url.Parse(`oracle://host/service?wallet=C%3A%5Cwallet`)
	err := oraclenet.PrepareEasyConnectWallet(parsed, "")
	if runtime.GOOS != "windows" && err == nil {
		t.Fatal("foreign Windows path became relative")
	}
	if runtime.GOOS == "windows" && err != nil {
		t.Fatal(err)
	}
}

func TestFinalUnicodeConfigurationLineCannotBeDiscarded(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "cwallet.sso"), "synthetic")
	for _, ending := range []string{"", "\n"} {
		write(t, filepath.Join(root, "sqlnet.ora"), "IFILE=钱包"+ending)
		if _, err := oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root); err == nil {
			t.Fatal("explicit unsupported IFILE silently selected a fallback wallet")
		}
		write(t, filepath.Join(root, "tnsnames.ora"), "DB=(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=host)(PORT=1521)))\u3000"+ending)
		if _, err := oraclenet.ResolveAlias("DB", root); err != nil {
			t.Fatal("final Unicode whitespace discarded the alias", err)
		}
	}
}
