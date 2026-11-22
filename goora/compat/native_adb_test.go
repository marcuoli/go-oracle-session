package compat_test

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/goora/compat"
	"github.com/marcuoli/go-oracle-session/inventory"
	"github.com/marcuoli/go-oracle-session/oraclenet"
)

// TestNativeADBMatrix runs both bundled drivers against three explicitly
// selected inventory profiles. It exercises session-local and temporary LOB state.
func TestNativeADBMatrix(t *testing.T) {
	if os.Getenv("ORACLE_COMPAT_ADB_STDIN") != "1" {
		t.Skip("native Autonomous Database matrix not enabled")
	}
	var input struct {
		InventoryPath, TNSAdmin, ProofDirectory, SourceRevision string
		WalletProfiles                                          []string
		TNSProfile                                              string
	}
	readNativeInput(t, &input)
	if len(input.WalletProfiles) != 2 || input.TNSProfile == "" || input.TNSAdmin == "" || input.SourceRevision == "" {
		t.Fatal("native ADB matrix requires source and TNS configuration, two wallet profiles and one TNS profile")
	}
	files, err := os.ReadDir(input.ProofDirectory)
	if err != nil || len(files) != 0 {
		t.Fatal("native ADB matrix requires an existing empty proof directory")
	}
	entries, err := inventory.ReadFile(input.InventoryPath)
	if err != nil {
		t.Fatal("cannot read private native ADB inventory")
	}
	byName := make(map[string]inventory.Entry, len(entries))
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	profiles := []struct {
		label, inventoryName string
		resolveAlias         bool
	}{
		{"wallet-1", input.WalletProfiles[0], false},
		{"wallet-2", input.WalletProfiles[1], false},
		{"tns-alias", input.TNSProfile, true},
	}
	seen := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if profile.inventoryName == "" || seen[profile.inventoryName] {
			t.Fatal("native ADB matrix requires three distinct inventory profiles")
		}
		seen[profile.inventoryName] = true
	}
	for _, profile := range profiles {
		entry := byName[profile.inventoryName]
		dsn, err := nativeADBURL(entry, input.TNSAdmin, profile.resolveAlias)
		for _, mode := range []compat.Mode{compat.V2, compat.V3} {
			name := profile.label + "-" + mode.String()
			t.Run(name, func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				cfg := nativeCompatibilityConfig{
					DSN: dsn, Mode: mode, SourceRevision: input.SourceRevision,
					ProofPath:       filepath.Join(input.ProofDirectory, name+".json"),
					ExtendedVersion: true, Container: true, adb: true,
				}
				cfg.Expected.User = entry.Username
				cfg.Expected.IsDBA = "FALSE"
				runNativeCompatibility(t, cfg)
			})
		}
	}
}

func nativeADBURL(entry inventory.Entry, tnsAdmin string, resolveAlias bool) (string, error) {
	if entry.Name == "" {
		return "", errors.New("selected ADB profile is missing from the private inventory")
	}
	user := url.UserPassword(entry.Username, entry.Password)
	if resolveAlias {
		descriptor, err := oraclenet.ResolveAlias(entry.Connect, tnsAdmin)
		if err != nil {
			return "", errors.New("cannot resolve the private ADB TNS alias")
		}
		wallet, err := oraclenet.ResolveWallet(descriptor, tnsAdmin)
		if err != nil || wallet == "" {
			return "", errors.New("cannot resolve the private ADB TNS wallet")
		}
		query := url.Values{"connStr": {descriptor}, "wallet": {wallet}, "SSL": {"enable"}}
		// Descriptor mode uses the driver's :0 authority so only the descriptor
		// supplies listener addresses. An empty authority is rejected before dialing.
		return (&url.URL{Scheme: "oracle", User: user, Host: ":0", Path: "/", RawQuery: query.Encode()}).String(), nil
	}
	parsed, err := url.Parse(entry.Connect)
	if err != nil || parsed.Scheme != "oracle" || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("selected ADB profile requires a direct wallet URL")
	}
	if err := oraclenet.PrepareEasyConnectWallet(parsed, tnsAdmin); err != nil {
		return "", errors.New("cannot resolve the private ADB URL wallet")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", errors.New("selected ADB profile has an invalid URL query")
	}
	var hasWallet bool
	for key, values := range query {
		switch strings.ToUpper(key) {
		case "TRACE FILE", "TRACE DIR", "TRACE DIRECTORY", "TRACE FOLDER":
			return "", errors.New("packet tracing is disabled in the native ADB matrix")
		case "CONNSTR":
			return "", errors.New("selected ADB profile requires a direct wallet URL")
		case "WALLET":
			if hasWallet || values[0] == "" {
				return "", errors.New("selected ADB profile requires one nonempty wallet option")
			}
			hasWallet = true
		}
	}
	if !hasWallet {
		return "", errors.New("selected ADB profile has no wallet")
	}
	parsed.User = user
	return parsed.String(), nil
}
