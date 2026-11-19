package main

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/marcuoli/go-oracle-session/goora/compat"
	official "github.com/sijms/go-ora/v3"
)

func TestOfficialDriverRegistrationCoexists(t *testing.T) {
	for _, name := range []string{"oracle", compat.DriverName} {
		if !slices.Contains(sql.Drivers(), name) {
			t.Fatalf("driver %q missing", name)
		}
	}
	if compat.IsDriver(official.NewConnector("unused").Driver()) {
		t.Fatal("official driver mislabeled")
	}
	for _, mode := range []compat.Mode{compat.V2, compat.V3, compat.Negotiated} {
		connector, err := compat.NewConnector("oracle://fixture:unused@127.0.0.1:1/fixture", compat.Options{Mode: mode})
		if err != nil || !compat.IsDriver(connector.Driver()) || connector.Mode() != mode {
			t.Fatalf("factory policy=%s err=%v", mode, err)
		}
	}
	number, err := compat.NewNumber("9007199254740993")
	if err != nil {
		t.Fatal(err)
	}
	if text, err := number.String(); err != nil || text != "9007199254740993" {
		t.Fatalf("NUMBER=%s %v", text, err)
	}
	var output compat.Clob
	if err := output.SetValue("é漢"); err != nil {
		t.Fatal(err)
	}
	if value, err := output.Value(); err != nil || value != "é漢" {
		t.Fatalf("CLOB=%v %v", value, err)
	}
	if err := output.Free(); err != nil {
		t.Fatal(err)
	}
}
