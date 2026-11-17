package oraclenet_test

import (
	"github.com/marcuoli/go-oracle-session/oraclenet"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSecuritySettingsRetainPolicyWithoutInterpretingQuotedDecoys(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "sqlnet.ora"), "SSL_SERVER_DN_MATCH=yes\nSSL_SERVER_CERT_DN=\"CN=file-pin,O=Example\"\nWALLET_LOCATION=(SOURCE=(METHOD=file)(METHOD_DATA=(DIRECTORY=wallet)))")
	descriptor := `(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME="(SECURITY=(SSL_VERSION=decoy))"))(SECURITY=(SSL_SERVER_DN_MATCH=yes)(SSL_SERVER_CERT_DN="CN=expected,O=Example")(MY_WALLET_DIRECTORY="wallet (SSL_VERSION=decoy)")))`
	settings, err := oraclenet.ReadSecuritySettings(descriptor, "  "+root+"  ")
	if err != nil {
		t.Fatal(err)
	}
	want := []oraclenet.SecuritySetting{
		{Name: "SSL_SERVER_DN_MATCH", Value: "yes", Descriptor: true},
		{Name: "SSL_SERVER_CERT_DN", Value: "CN=expected,O=Example", Descriptor: true},
		{Name: "MY_WALLET_DIRECTORY", Value: "wallet (SSL_VERSION=decoy)", Descriptor: true},
		{Name: "SSL_SERVER_DN_MATCH", Value: "yes"},
		{Name: "SSL_SERVER_CERT_DN", Value: "CN=file-pin,O=Example"},
	}
	if !slices.Equal(settings, want) {
		t.Fatalf("security values/order/origin changed: got%+v want%+v", settings, want)
	}
}
func TestSecuritySettingsPreserveDuplicatesAndMissingFile(t *testing.T) {
	settings, err := oraclenet.ReadSecuritySettings(`(DESCRIPTION=(SECURITY=(SSL_SERVER_DN_MATCH=yes)(SSL_SERVER_DN_MATCH=no)))`, t.TempDir())
	want := []oraclenet.SecuritySetting{
		{Name: "SSL_SERVER_DN_MATCH", Value: "yes", Descriptor: true},
		{Name: "SSL_SERVER_DN_MATCH", Value: "no", Descriptor: true},
	}
	if err != nil || !slices.Equal(settings, want) {
		t.Fatal(settings, err)
	}
}

func TestSecuritySettingsRejectIncludeEvenWhenWalletLocationIsPresent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "sqlnet.ora"), "WALLET_LOCATION=(SOURCE=(METHOD=file)(METHOD_DATA=(DIRECTORY=wallet)))\nIFILE=constraints.ora")
	write(t, filepath.Join(root, "constraints.ora"), `SSL_SERVER_CERT_DN="CN=expected,O=Example"`)
	wallet, err := oraclenet.ResolveWallet("(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)))", root)
	if err != nil || wallet != filepath.Join(root, "wallet") {
		t.Fatalf("explicit wallet location changed: %q, %v", wallet, err)
	}
	settings, err := oraclenet.ReadSecuritySettings(`(DESCRIPTION=(SECURITY=(SSL_SERVER_DN_MATCH=yes)))`, root)
	if err == nil || settings != nil {
		t.Fatal("uninspected included policy appeared complete", settings, err)
	}
}

func TestSecuritySettingsTraverseDescriptionsAndOwnResults(t *testing.T) {
	descriptor := `(DESCRIPTION_LIST=(DESCRIPTION=(CONNECT_DATA=(SERVICE_NAME="(SECURITY=(SSL_VERSION=decoy))"))(SECURITY=(SSL_VERSION="1.2")(SSL_VERSION="1.3")))(DESCRIPTION=(SECURITY=(SSL_SERVER_CERT_DN="CN=(SSL_VERSION=decoy)#,O=日本"))))`
	want := []oraclenet.SecuritySetting{
		{Name: "SSL_VERSION", Value: "1.2", Descriptor: true},
		{Name: "SSL_VERSION", Value: "1.3", Descriptor: true},
		{Name: "SSL_SERVER_CERT_DN", Value: "CN=(SSL_VERSION=decoy)#,O=日本", Descriptor: true},
	}
	got, err := oraclenet.ReadSecuritySettings(descriptor, "")
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("ReadSecuritySettings() = %#v, %v; want %#v", got, err, want)
	}
	got[0].Value = "caller mutation"
	again, err := oraclenet.ReadSecuritySettings(descriptor, "")
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("caller mutation affected another result: %#v, %v", again, err)
	}
}

func TestSecuritySettingsRejectBoundsWithoutPrefix(t *testing.T) {
	for _, tc := range []struct{ name, descriptor string }{
		{"descriptor bytes", strings.Repeat(" ", (1<<20)+1)},
		{"descriptor depth", strings.Repeat("(DESCRIPTION=", 18) + "(SECURITY=(SSL_VERSION=1.2))" + strings.Repeat(")", 18)},
		{"malformed tail", "(DESCRIPTION=(SECURITY=(SSL_VERSION=1.2)))(DESCRIPTION=(SECURITY=unterminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oraclenet.ReadSecuritySettings(tc.descriptor, "")
			if err == nil || got != nil {
				t.Fatalf("invalid descriptor returned a prefix: %#v, %v", got, err)
			}
		})
	}
	t.Run("oversized file invalidates descriptor prefix", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "sqlnet.ora"), strings.Repeat(" ", (1<<20)+1))
		got, err := oraclenet.ReadSecuritySettings("(DESCRIPTION=(SECURITY=(SSL_VERSION=1.2)))", root)
		if err == nil || got != nil {
			t.Fatalf("invalid file returned a descriptor prefix: %#v, %v", got, err)
		}
	})
}
