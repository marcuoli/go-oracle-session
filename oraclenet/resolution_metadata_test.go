package oraclenet_test

import (
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/oraclenet"
)

func TestDescribeConnectInput(t *testing.T) {
	admin := filepath.Join(t.TempDir(), "unused")
	configured := " " + admin + " "
	for _, test := range []struct {
		name, connect, directory string
		want                     oraclenet.ConnectInput
	}{
		{"host and service", " db.example.invalid/service ", admin, oraclenet.ConnectInput{Kind: oraclenet.ConnectInputEasyConnect}},
		{"URL syntax only", "oracle://user:secret@db.example.invalid/service", admin, oraclenet.ConnectInput{Kind: oraclenet.ConnectInputEasyConnect}},
		{"alias", " db_high.example ", configured, oraclenet.ConnectInput{
			Kind: oraclenet.ConnectInputTNSAlias, Alias: "db_high.example", TNSAdmin: configured,
			TNSNamesPath: filepath.Join(configured, "tnsnames.ora"),
		}},
		{"alias without directory", "DB_HIGH", "  ", oraclenet.ConnectInput{
			Kind: oraclenet.ConnectInputTNSAlias, Alias: "DB_HIGH", TNSAdmin: "  ",
		}},
		{"invalid alias", "invalid alias", admin, oraclenet.ConnectInput{Kind: oraclenet.ConnectInputInvalid}},
		{"blank", "   ", admin, oraclenet.ConnectInput{Kind: oraclenet.ConnectInputInvalid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := oraclenet.DescribeConnectInput(test.connect, test.directory); got != test.want {
				t.Fatalf("metadata = %+v, want %+v", got, test.want)
			}
		})
	}

	input := oraclenet.DescribeConnectInput("Missing", admin)
	_, err := oraclenet.ResolveAlias(input.Alias, input.TNSAdmin)
	var failure *oraclenet.ResolutionError
	if !errors.As(err, &failure) || failure.Reason != oraclenet.TNSAdminUnavailable ||
		failure.Alias != input.Alias || failure.Path != admin {
		t.Fatalf("unavailable directory: failure = %+v, error = %v", failure, err)
	}
}

func TestEasyConnectWalletPaths(t *testing.T) {
	admin := filepath.Join(t.TempDir(), "wallet, ação")
	t.Setenv("SDK_TEST_EMPTY_WALLET", "")
	walletOption := url.QueryEscape("${TNS_ADMIN}")
	for _, test := range []struct {
		name, connect string
		want          []string
		wantErr       bool
	}{
		{"without wallet", "db.example.invalid/service?TLS=true&password=secret", nil, false},
		{"alias handled separately", "DB_HIGH", nil, false},
		{"ignore URL userinfo", "oracle://user:secret@db.example.invalid/service?wallet=option", nil, false},
		{"wrong scheme", "https://db.example.invalid/service?wallet=option", nil, false},
		{"without service", "oracle://db.example.invalid/?wallet=option", nil, false},
		{"expanded", "//db.example.invalid/service?WaLlEt=" + walletOption + "&password=secret", []string{admin}, false},
		{"first value per spelling", "db.example.invalid/service?%77allet=first&wallet=ignored&WALLET=second", []string{"first", "second"}, false},
		{"invalid encoded wallet", "db.example.invalid/service?wallet=%2", nil, true},
		{"invalid variable", "db.example.invalid/service?wallet=${SDK_TEST_EMPTY_WALLET}", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := oraclenet.EasyConnectWalletPaths(test.connect, admin)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error = %t", err, test.wantErr)
			}
			slices.Sort(got)
			if !slices.Equal(got, test.want) {
				t.Fatalf("wallet paths = %q, want %q", got, test.want)
			}
			if test.wantErr {
				parsed, parseErr := url.Parse("oracle://" + test.connect)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				preparationErr := oraclenet.PrepareEasyConnectWallet(parsed, admin)
				if preparationErr == nil || err.Error() != preparationErr.Error() || strings.Contains(err.Error(), "password=secret") {
					t.Fatalf("metadata failure = %v, preparation failure = %v", err, preparationErr)
				}
			}
		})
	}
}
