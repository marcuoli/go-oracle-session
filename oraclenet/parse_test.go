package oraclenet_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/oraclenet"
)

func TestParseTNSNamesPreservesEffectiveAliasesAndIncludeDeclarations(t *testing.T) {
	input := []byte("# comment\r\nzeta, other=(DESCRIPTION=(HOST=old))\r\nIFILE=\"missing-include.ora\"\r\nalpha=(DESCRIPTION=(HOST=\"quoted#(host)\")) # comment\r\nZETA=(DESCRIPTION=(HOST=new))\r\n")
	got, err := oraclenet.ParseTNSNames(input)
	want := oraclenet.TNSNamesConfig{Entries: []oraclenet.TNSName{{Name: "ALPHA", Descriptor: `(DESCRIPTION=(HOST="quoted#(host)"))`}, {Name: "OTHER", Descriptor: "(DESCRIPTION=(HOST=old))"}, {Name: "ZETA", Descriptor: "(DESCRIPTION=(HOST=new))"}}, Includes: []oraclenet.NetInclude{{Value: `"missing-include.ora"`}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
	encoded, err := json.Marshal(got)
	if err != nil || !strings.Contains(string(encoded), `"descriptor":`) || !strings.Contains(string(encoded), `"includes":`) {
		t.Fatal(string(encoded), err)
	}
}

func TestParseSQLNetPreservesAssignmentOrderDuplicatesAndLiteralValues(t *testing.T) {
	got, err := oraclenet.ParseSQLNet([]byte("# comment\nSSL_VERSION=1.2\nIFILE=\"$UNSET/missing#(file).ora\" # no expansion\nSSL_VERSION=1.3\nWALLET_LOCATION=(SOURCE=(METHOD=FILE)(METHOD_DATA=(DIRECTORY=\"opaque#(path)\")))\n"))
	want := oraclenet.SQLNetConfig{Entries: []oraclenet.NetSetting{{Name: "SSL_VERSION", Value: "1.2"}, {Name: "IFILE", Value: `"$UNSET/missing#(file).ora"`}, {Name: "SSL_VERSION", Value: "1.3"}, {Name: "WALLET_LOCATION", Value: `(SOURCE=(METHOD=FILE)(METHOD_DATA=(DIRECTORY="opaque#(path)")))`}}, Includes: []oraclenet.NetInclude{{Value: `"$UNSET/missing#(file).ora"`}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestPureNetParsingRejectsInvalidInputWithoutPrefixesOrPrivateErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"utf8", []byte{0xff}, oraclenet.ErrNetConfigEncoding},
		{"nul", []byte("SECRET\x00"), oraclenet.ErrNetConfigEncoding},
		{"capacity", []byte(strings.Repeat("s", (1<<20)+1)), oraclenet.ErrNetConfigCapacity},
		{"syntax", []byte("GOOD=(DESCRIPTION=(HOST=valid))\nSECRET=(unterminated"), oraclenet.ErrNetConfigSyntax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tns, err := oraclenet.ParseTNSNames(tc.data)
			if !errors.Is(err, tc.want) || len(tns.Entries) != 0 || len(tns.Includes) != 0 || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("TNS failure contract", err)
			}
			net, err := oraclenet.ParseSQLNet(tc.data)
			if !errors.Is(err, tc.want) || len(net.Entries) != 0 || len(net.Includes) != 0 || strings.Contains(err.Error(), "SECRET") {
				t.Fatal("sqlnet failure contract", err)
			}
		})
	}
	for _, text := range []string{"GOOD=(X=Y)\ntrailing private text", "OTHER=private-value", "IFILE=\"unterminated"} {
		if got, err := oraclenet.ParseTNSNames([]byte(text)); !errors.Is(err, oraclenet.ErrNetConfigSyntax) || len(got.Entries) != 0 {
			t.Fatal("unsupported TNS syntax looked complete", err)
		}
	}
}

func TestPureNetParsingSizeBoundaryAndEmptyIncludes(t *testing.T) {
	data := []byte(strings.Repeat(" ", 1<<20))
	if got, err := oraclenet.ParseTNSNames(data); err != nil || len(got.Entries) != 0 || len(got.Includes) != 0 {
		t.Fatal("TNS bound", err)
	}
	if got, err := oraclenet.ParseSQLNet(data); err != nil || len(got.Entries) != 0 || len(got.Includes) != 0 {
		t.Fatal("sqlnet bound", err)
	}
	for _, text := range []string{"IFILE=", `IFILE=""`} {
		if _, err := oraclenet.ParseTNSNames([]byte(text)); !errors.Is(err, oraclenet.ErrNetConfigSyntax) {
			t.Fatal("empty TNS include", err)
		}
		if _, err := oraclenet.ParseSQLNet([]byte(text)); !errors.Is(err, oraclenet.ErrNetConfigSyntax) {
			t.Fatal("empty sqlnet include", err)
		}
	}
}

func TestParseSQLNetRejectsIncompleteOrMultipleScalarTokens(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"opening_quote", `"1.2`},
		{"middle_quote", `1".2`},
		{"closing_quote", `1.2"`},
		{"third_quote", `"1.2""`},
		{"trailing_quoted_token", `"1.2" "tail"`},
		{"trailing_unquoted_token", `"1.2" tail`},
		{"embedded_quoted_token", `1."2"`},
		{"trailing_scalar_token", `one two`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oraclenet.ParseSQLNet([]byte("VALID_SCALAR=first\nTEST_SCALAR=" + tc.value + "\n"))
			if !errors.Is(err, oraclenet.ErrNetConfigSyntax) || len(got.Entries) != 0 || len(got.Includes) != 0 {
				t.Fatalf("malformed scalar returned a parsed prefix: %#v, %v", got, err)
			}
			gotTNS, err := oraclenet.ParseTNSNames([]byte("VALID=(DESCRIPTION=(HOST=example.invalid))\nIFILE=" + tc.value + "\n"))
			if !errors.Is(err, oraclenet.ErrNetConfigSyntax) || len(gotTNS.Entries) != 0 || len(gotTNS.Includes) != 0 {
				t.Fatalf("malformed include scalar returned a parsed prefix: %#v, %v", gotTNS, err)
			}
		})
	}
}

func TestParseSQLNetRetainsSupportedWholeScalarsAndComments(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"unquoted", "TEST_SCALAR=literal.token # comment\n", "literal.token"},
		{"quoted", "TEST_SCALAR=\"literal with whitespace\" # comment\n", `"literal with whitespace"`},
		{"quoted_hash", "TEST_SCALAR=\"literal#(value)\" # comment with unmatched \"\n", `"literal#(value)"`},
		{"comment_quote", "TEST_SCALAR=literal # comment with unmatched \"\n", "literal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oraclenet.ParseSQLNet([]byte(tc.input))
			want := oraclenet.SQLNetConfig{Entries: []oraclenet.NetSetting{{Name: "TEST_SCALAR", Value: tc.want}}}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v, %v; want %#v", got, err, want)
			}
		})
	}
}
