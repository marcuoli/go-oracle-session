package oraclenet_test

import (
	"github.com/marcuoli/go-oracle-session/oraclenet"
	"path/filepath"
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
	if len(settings) != 5 {
		t.Fatal(settings)
	}
	if settings[1].Name != "SSL_SERVER_CERT_DN" || settings[1].Value != "CN=expected,O=Example" || !settings[1].Descriptor {
		t.Fatal(settings)
	}
	if settings[4].Value != "CN=file-pin,O=Example" || settings[4].Descriptor {
		t.Fatal(settings)
	}
}
func TestSecuritySettingsPreserveDuplicatesAndMissingFile(t *testing.T) {
	settings, err := oraclenet.ReadSecuritySettings(`(DESCRIPTION=(SECURITY=(SSL_SERVER_DN_MATCH=yes)(SSL_SERVER_DN_MATCH=no)))`, t.TempDir())
	if err != nil || len(settings) != 2 || settings[0].Value != "yes" || settings[1].Value != "no" {
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
