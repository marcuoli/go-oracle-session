package oraclenet_test

import (
	"errors"
	"testing"

	"github.com/marcuoli/go-oracle-session/oraclenet"
)

func TestFormatConnectStrings(t *testing.T) {
	for _, tc := range []struct {
		name, reference, alias string
		wallet                 bool
		want                   oraclenet.ConnectStrings
	}{
		{
			name: "TCP service", reference: "oracle://db.example.invalid:1521/app", alias: "APP",
			want: oraclenet.ConnectStrings{
				EasyConnect: "db.example.invalid:1521/app\n//db.example.invalid:1521/app",
				TNSNames:    "APP = (DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=db.example.invalid)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=app)))",
				JDBCThin:    "jdbc:oracle:thin:@//db.example.invalid:1521/app",
			},
		},
		{
			name: "TCPS IPv6 service override", reference: "tcps://[2001:db8::A]:1522/ignored?SERVICE+NAME=app.example", alias: "APP",
			want: oraclenet.ConnectStrings{
				EasyConnect: "tcps://[2001:db8::A]:1522/app.example",
				TNSNames:    "APP = (DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=2001:db8::A)(PORT=1522))(CONNECT_DATA=(SERVICE_NAME=app.example))(SECURITY=(SSL_SERVER_DN_MATCH=YES)))",
				JDBCThin:    "jdbc:oracle:thin:@tcps://[2001:db8::A]:1522/app.example",
			},
		},
		{
			name: "SID takes precedence", reference: "tcp://[2001:db8::2]:1522/old?SID=XE&service+name=other", alias: "XE",
			want: oraclenet.ConnectStrings{
				TNSNames: "XE = (DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=2001:db8::2)(PORT=1522))(CONNECT_DATA=(SID=XE)))",
				JDBCThin: "jdbc:oracle:thin:@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCP)(HOST=[2001:db8::2])(PORT=1522))(CONNECT_DATA=(SID=XE)))",
			},
		},
		{
			name: "opaque alias", reference: "adb_high", alias: "NO_DESCRIPTOR",
			want: oraclenet.ConnectStrings{JDBCThin: "jdbc:oracle:thin:@adb_high"},
		},
		{
			name: "wallet alias", reference: "adb_high", wallet: true,
			want: oraclenet.ConnectStrings{JDBCThin: "jdbc:oracle:thin:@adb_high?TNS_ADMIN=<wallet-directory>"},
		},
		{
			name: "wallet TCPS", reference: "tcps://db.example.invalid:1521/app", alias: "APP", wallet: true,
			want: oraclenet.ConnectStrings{
				EasyConnect: `tcps://db.example.invalid:1521/app?wallet_location="<wallet-directory>"`,
				TNSNames:    `APP = (DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=db.example.invalid)(PORT=1521))(CONNECT_DATA=(SERVICE_NAME=app))(SECURITY=(SSL_SERVER_DN_MATCH=YES)(WALLET_LOCATION="<wallet-directory>")))`,
				JDBCThin:    `jdbc:oracle:thin:@tcps://db.example.invalid:1521/app?wallet_location="<wallet-directory>"`,
			},
		},
		{
			name: "wallet SID without entry", reference: "tcps://db.example.invalid:1522/?SID=XE", wallet: true,
			want: oraclenet.ConnectStrings{
				JDBCThin: `jdbc:oracle:thin:@(DESCRIPTION=(ADDRESS=(PROTOCOL=TCPS)(HOST=db.example.invalid)(PORT=1522))(CONNECT_DATA=(SID=XE))(SECURITY=(SSL_SERVER_DN_MATCH=YES)(WALLET_LOCATION="<wallet-directory>")))`,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oraclenet.FormatConnectStrings(tc.reference, tc.alias, oraclenet.ConnectStringOptions{WalletTemplate: tc.wallet})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("connection strings = %+v, want %+v", got, tc.want)
			}
			if got.TNSNames != "" {
				parsed, err := oraclenet.ParseTNSNames([]byte(got.TNSNames))
				if err != nil || len(parsed.Entries) != 1 || parsed.Entries[0].Name != tc.alias || len(parsed.Includes) != 0 {
					t.Fatalf("generated TNS entry parsed as %+v, error %v", parsed, err)
				}
			}
		})
	}
}

func TestFormatConnectStringsRejectsUnsafeInput(t *testing.T) {
	for _, tc := range []struct {
		name, reference, alias string
		wallet                 bool
	}{
		{name: "credentials", reference: "oracle://demo:reject-me@db.example.invalid:1521/app"},
		{name: "local path", reference: "tcps://db.example.invalid:1522/app?WALLET=C%3A%2Freject-me"},
		{name: "fragment", reference: "oracle://db.example.invalid:1521/app#reject-me"},
		{name: "descriptor input", reference: "(DESCRIPTION=(ADDRESS=(HOST=reject-me)))"},
		{name: "combined routes", reference: "tcp://one.example.invalid:1521/a; tcps://two.example.invalid:1522/b"},
		{name: "missing port", reference: "oracle://db.example.invalid/app"},
		{name: "invalid port", reference: "oracle://db.example.invalid:65536/app"},
		{name: "zero port", reference: "oracle://db.example.invalid:0/app"},
		{name: "missing database identifier", reference: "tcp://db.example.invalid:1521/"},
		{name: "unsupported protocol", reference: "http://db.example.invalid:1521/app"},
		{name: "duplicate SID", reference: "oracle://db.example.invalid:1521/?SID=APP&sid=OTHER"},
		{name: "repeated SID key", reference: "oracle://db.example.invalid:1521/?SID=APP&SID=OTHER"},
		{name: "duplicate service", reference: "tcp://db.example.invalid:1521/?SERVICE+NAME=APP&service+name=OTHER"},
		{name: "empty service override", reference: "tcp://db.example.invalid:1521/app?SERVICE+NAME="},
		{name: "encoded service syntax", reference: "tcp://db.example.invalid:1521/app%29%28SID=other"},
		{name: "encoded SID newline", reference: "tcp://db.example.invalid:1521/?SID=XE%0AOTHER"},
		{name: "scoped IPv6", reference: "tcp://[fe80::1%25eth0]:1521/app"},
		{name: "no wallet promotion", reference: "tcp://db.example.invalid:1522/app", wallet: true},
		{name: "invalid entry alias", reference: "tcp://db.example.invalid:1521/app", alias: "APP\nOTHER"},
		{name: "reserved entry alias", reference: "tcp://db.example.invalid:1521/app", alias: "ifile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oraclenet.FormatConnectStrings(tc.reference, tc.alias, oraclenet.ConnectStringOptions{WalletTemplate: tc.wallet})
			if got != (oraclenet.ConnectStrings{}) || !errors.Is(err, oraclenet.ErrInvalidConnectStringInput) || err.Error() != "invalid Oracle Net connection string input" {
				t.Fatalf("got %+v, %v, want zero output and the static input error", got, err)
			}
		})
	}
}
