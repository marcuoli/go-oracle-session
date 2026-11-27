package go_ora

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/converters"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
	oraTypes "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

func TestPLSQLScalarCharacterBindPreservesVARCHAR2(t *testing.T) {
	const plsql = "DECLARE v VARCHAR2(32767) := :1; BEGIN NULL; END;"
	const query = "SELECT :1 FROM DUAL"
	long := strings.Repeat("é漢", 1000)
	for _, test := range []struct {
		name      string
		version   *DBVersion
		statement string
		direction ParameterDirection
		input     string
		size      int
		want      uint16
	}{
		{"9.2", &DBVersion{MajorVersion: 9, MinorVersion: 2}, plsql, Input, long, 0, oraTypes.NCHAR},
		{"10.2", &DBVersion{MajorVersion: 10, MinorVersion: 2}, plsql, Input, long, 0, oraTypes.NCHAR},
		{"11.2", &DBVersion{MajorVersion: 11, MinorVersion: 2}, plsql, Input, long, 0, oraTypes.NCHAR},
		{"12.1", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, long, 0, oraTypes.NCHAR},
		{"SQL limit", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, strings.Repeat("a", 4000), 0, oraTypes.NCHAR},
		{"above SQL limit", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, strings.Repeat("a", 4001), 0, oraTypes.NCHAR},
		{"PLSQL limit", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, strings.Repeat("é漢", 6553) + "aa", 0, oraTypes.NCHAR},
		{"above PLSQL limit", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, strings.Repeat("é漢", 6553) + "aaa", 0, oraTypes.LongVarChar},
		{"oversized declared input", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Input, long, 32768, oraTypes.LongVarChar},
		{"12.2", &DBVersion{MajorVersion: 12, MinorVersion: 2}, plsql, Input, long, 0, oraTypes.LongVarChar},
		{"future", &DBVersion{MajorVersion: 75}, plsql, Input, long, 0, oraTypes.LongVarChar},
		{"missing version", nil, plsql, Input, long, 0, oraTypes.LongVarChar},
		{"unknown version", &DBVersion{}, plsql, Input, long, 0, oraTypes.LongVarChar},
		{"SQL", &DBVersion{MajorVersion: 12, MinorVersion: 1}, query, Input, long, 0, oraTypes.LongVarChar},
		{"OUT", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, Output, long, 0, oraTypes.LongVarChar},
		{"IN OUT", &DBVersion{MajorVersion: 12, MinorVersion: 1}, plsql, InOut, long, 0, oraTypes.LongVarChar},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := scalarBindTestConnection(t, 873, test.version)
			par, err := NewStmt(test.statement, conn).NewParam("value", test.input, test.size, test.direction)
			if err != nil {
				t.Fatal(err)
			}
			if par.DataType != test.want {
				t.Fatalf("type = %d, want %d", par.DataType, test.want)
			}
			if par.MaxLen != int64(max(test.size, len(test.input))) {
				t.Fatalf("max length = %d", par.MaxLen)
			}
			if test.direction == Output {
				if par.BValue != nil {
					t.Fatal("OUT unexpectedly encodes an input value")
				}
			} else if !bytes.Equal(par.BValue, []byte(test.input)) {
				t.Fatal("bind changed encoded bytes")
			}
		})
	}
}

func TestPLSQLScalarCharacterBindUsesEncodedLength(t *testing.T) {
	conn := scalarBindTestConnection(t, 178, &DBVersion{MajorVersion: 11, MinorVersion: 2})
	const count = 16384 // WE8MSWIN1252 encodes é as one byte.
	stmt := NewStmt("DECLARE v VARCHAR2(32767) := :1; BEGIN NULL; END;", conn)
	par, err := stmt.NewParam("value", strings.Repeat("é", count), 0, Input)
	if err != nil {
		t.Fatal(err)
	}
	if par.DataType != oraTypes.NCHAR || par.MaxLen != count || !bytes.Equal(par.BValue, bytes.Repeat([]byte{0xe9}, count)) {
		t.Fatalf("type = %d, max length = %d, encoded length = %d", par.DataType, par.MaxLen, len(par.BValue))
	}
}

func TestPLSQLCharacterArrayRetainsFraming(t *testing.T) {
	conn := scalarBindTestConnection(t, 873, &DBVersion{MajorVersion: 11, MinorVersion: 2})
	stmt := NewStmt("BEGIN p(:1); END;", conn)
	par, err := stmt.NewParam("values", []string{strings.Repeat("a", 5000)}, 0, Input)
	if err != nil {
		t.Fatal(err)
	}
	if par.ArraySize != 1 || par.DataType != oraTypes.NCHAR {
		t.Fatalf("array size = %d, type = %d", par.ArraySize, par.DataType)
	}
}

func scalarBindTestConnection(t *testing.T, charset int, version *DBVersion) *Connection {
	t.Helper()
	config := &configurations.ConnectionConfig{CharsetID: charset}
	conn, err := NewConnection("", config)
	if err != nil {
		t.Fatal(err)
	}
	conn.State = Opened
	conn.tracer = trace.NilTracer()
	conn.session = network.NewSession(config, conn.tracer)
	conn.sStrConv = conn.cStrConv
	conn.buildParameterCoderMap(NewDriver())
	conn.maxLen.varchar = 4000
	conn.dBVersion = version
	return conn
}

func TestPLSQLTaggedScalarCharacterBind(t *testing.T) {
	type args struct {
		Value string `db:"value,varchar"`
	}
	value := args{Value: strings.Repeat("a", 4001)}
	for _, test := range []struct {
		name  string
		value any
	}{
		{"struct", value},
		{"pointer", &value},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := scalarBindTestConnection(t, 873, &DBVersion{MajorVersion: 12, MinorVersion: 1})
			stmt := NewStmt("BEGIN p(:value); END;", conn)
			count, err := stmt.structPar(test.value, 0)
			if err != nil || count != 1 || len(stmt.Pars) != 1 {
				t.Fatalf("encoded %d fields, got %d parameters, error %v", count, len(stmt.Pars), err)
			}
			par := stmt.Pars[0]
			if par.DataType != oraTypes.NCHAR || par.MaxLen != 4001 || string(par.BValue) != value.Value {
				t.Fatalf("tagged bind type = %d, max length = %d, encoded length = %d", par.DataType, par.MaxLen, len(par.BValue))
			}
		})
	}
}

func TestPLSQLTaggedNationalCharacterBindRetainsFraming(t *testing.T) {
	conn := scalarBindTestConnection(t, 873, &DBVersion{MajorVersion: 12, MinorVersion: 1})
	conn.nStrConv = converters.NewStringConverter(2000) // AL16UTF16
	stmt := NewStmt("BEGIN p(:value); END;", conn)
	value := struct {
		Value string `db:"value,nvarchar"`
	}{Value: strings.Repeat("a", 2500)}
	count, err := stmt.structPar(value, 0)
	if err != nil || count != 1 || len(stmt.Pars) != 1 {
		t.Fatalf("encoded %d fields, got %d parameters, error %v", count, len(stmt.Pars), err)
	}
	par := stmt.Pars[0]
	if par.CharsetForm != 2 || par.DataType != oraTypes.LongVarChar || par.MaxLen != 5000 {
		t.Fatalf("national bind form = %d, type = %d, max length = %d", par.CharsetForm, par.DataType, par.MaxLen)
	}
}
