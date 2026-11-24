package oraclenet_test

import (
	"errors"
	"io/fs"
	"path/filepath"
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

	dir := t.TempDir()
	_, err = oraclenet.ResolveAlias("APP", dir)
	var resolution *oraclenet.ResolutionError
	if !errors.As(err, &resolution) || resolution.Reason != oraclenet.TNSFileUnavailable ||
		!errors.Is(err, oraclenet.ErrTNSAliasUnavailable) || !errors.Is(err, netconfig.ErrTNSAliasUnavailable) {
		t.Fatalf("SDK alias error = %v, resolution = %+v", err, resolution)
	}
	wantPath := filepath.Join(dir, "tnsnames.ora")
	if resolution.Alias != "APP" || resolution.Path != wantPath || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("SDK alias error has alias %q, path %q, cause %v; want APP, %q and a missing file",
			resolution.Alias, resolution.Path, resolution.Cause, wantPath)
	}

	_, err = oraclenet.FormatConnectStrings("unrecognized://bad", "APP", opts)
	if err != oraclenet.ErrInvalidConnectStringInput || err != netconfig.ErrInvalidConnectStringInput {
		t.Fatalf("SDK formatter error differs: %v", err)
	}
}
