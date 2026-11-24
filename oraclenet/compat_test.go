package oraclenet_test

import (
	"errors"
	"testing"

	netconfig "github.com/marcuoli/go-oracle-net"
	"github.com/marcuoli/go-oracle-session/oraclenet"
)

func TestOracleNetCompatibility(t *testing.T) {
	const reference = "oracle://db.example.invalid:1521/app"
	opts := netconfig.ConnectStringOptions{IndentTNSNames: true, CompactTNSNamesSections: true}
	want, err := netconfig.FormatConnectStrings(reference, "APP", opts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := oraclenet.FormatConnectStrings(reference, "APP", opts)
	if err != nil || got != want {
		t.Fatalf("SDK formats = %+v, error %v; want %+v", got, err, want)
	}
	config, err := oraclenet.ParseTNSNames([]byte(got.TNSNames))
	if err != nil || len(config.Entries) != 1 || config.Entries[0].Name != "APP" {
		t.Fatalf("SDK TNS parse = %+v, error %v", config, err)
	}

	_, err = oraclenet.ResolveAlias("APP", t.TempDir())
	var resolution *netconfig.ResolutionError
	if !errors.As(err, &resolution) || resolution.Reason != oraclenet.TNSFileUnavailable ||
		!errors.Is(err, oraclenet.ErrTNSAliasUnavailable) || !errors.Is(err, netconfig.ErrTNSAliasUnavailable) {
		t.Fatalf("SDK alias error = %v, resolution = %+v", err, resolution)
	}
	var oldResolution *oraclenet.ResolutionError = resolution
	if oldResolution != resolution {
		t.Fatalf("SDK resolution error differs: %v", oldResolution)
	}

	_, err = oraclenet.FormatConnectStrings("unrecognized://bad", "APP", opts)
	if err != oraclenet.ErrInvalidConnectStringInput || err != netconfig.ErrInvalidConnectStringInput {
		t.Fatalf("SDK formatter error differs: %v", err)
	}
}
