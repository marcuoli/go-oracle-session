package oraclenet_test

import (
	"github.com/marcuoli/go-oracle-session/oraclenet"
	"path/filepath"
	"slices"
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
	settings, err := oraclenet.ReadSecuritySettings(`(DESCRIPTION=(SECURITY=(SSL_SERVER_DN_MATCH=yes)))`, root)
	if err == nil || settings != nil {
		t.Fatal("uninspected included policy appeared complete", settings, err)
	}
}
