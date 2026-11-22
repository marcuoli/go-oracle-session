package go_ora

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/trace"
)

// Legacy PL/SQL can continue the bind row with another TTC7 after a character
// OUT buffer larger than the SQL VARCHAR2 limit. The marker is not a CLR size.
func TestLegacyLargeCharacterOutputContinuesAtNextBind(t *testing.T) {
	for _, legacy := range []bool{true, false} {
		conn, err := NewConnection("", &configurations.ConnectionConfig{CharsetID: 873})
		if err != nil {
			t.Fatal(err)
		}
		conn.sStrConv = conn.cStrConv
		conn.tracer = trace.NilTracer()
		conn.State = Opened
		conn.maxLen.varchar = 4000
		version := uint16(9208)
		if !legacy {
			version = 10102
		}
		conn.dBVersion = &DBVersion{Number: version, MajorVersion: 10}
		if legacy {
			conn.dBVersion.MajorVersion = 9
		}
		var first, number string
		var null sql.NullString
		stmt := NewStmt("BEGIN :1 := :2; :3 := '9007199254740993'; :4 := NULL; END;", conn)
		stmt.containOutputPars = true
		stmt.Pars = []ParameterInfo{
			{DataType: NCHAR, Direction: Output, MaxLen: 131068, MaxCharLen: 32767, CharsetID: 873, Value: &first},
			{DataType: NCHAR, Direction: Input, MaxLen: 7, MaxCharLen: 7, CharsetID: 873, Value: "é漢-0"},
			{DataType: NCHAR, Direction: Output, MaxLen: 256, MaxCharLen: 64, CharsetID: 873, Value: &number},
			{DataType: NCHAR, Direction: Output, MaxLen: 256, MaxCharLen: 64, CharsetID: 873, Value: &null},
		}
		var response bytes.Buffer
		response.WriteByte(7)
		writer := network.NewSessionWithInputBufferForDebug(nil)
		writer.WriteClr(&response, []byte("é漢-0"))
		response.WriteByte(0)
		if legacy {
			response.WriteByte(7)
		}
		writer.WriteClr(&response, []byte("9007199254740993"))
		response.WriteByte(0)
		response.Write([]byte{0, 0x81, 1, 9, 0x55}) // NULL, indicator-1, end of call, next-message sentinel
		conn.session = network.NewSessionWithInputBufferForDebug(response.Bytes())
		if err := stmt.read(&ResultSet{}); err != nil {
			t.Fatalf("legacy=%v: %v", legacy, err)
		}
		if stmt.Pars[0].oPrimValue != "é漢-0" || stmt.Pars[2].oPrimValue != "9007199254740993" || stmt.Pars[3].oPrimValue != nil {
			t.Fatalf("legacy=%v output values: %v %v %v", legacy, stmt.Pars[0].oPrimValue, stmt.Pars[2].oPrimValue, stmt.Pars[3].oPrimValue)
		}
		next, err := conn.session.GetByte()
		if err != nil || next != 0x55 {
			t.Fatalf("legacy=%v consumed next message: %x %v", legacy, next, err)
		}
	}
}

func legacyOutputConnection(t *testing.T) *Connection {
	t.Helper()
	conn, err := NewConnection("", &configurations.ConnectionConfig{CharsetID: 873})
	if err != nil {
		t.Fatal(err)
	}
	conn.State = Opened
	conn.sStrConv = conn.cStrConv
	conn.tracer = trace.NilTracer()
	conn.maxLen.varchar = 4000
	conn.dBVersion = &DBVersion{MajorVersion: 9, Number: 9208}
	conn.session = network.NewSessionWithInputBufferForDebug(nil)
	return conn
}
func TestLegacyContinuationRequiresAllRemainingOutputs(t *testing.T) {
	for _, tc := range []struct {
		name               string
		tail               ParameterDirection
		hasTail, wantError bool
	}{
		{"missing output", Output, true, true}, {"last output", Input, false, false}, {"trailing input", Input, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := legacyOutputConnection(t)
			first, second := "previous-first", "previous-second"
			stmt := NewStmt("BEGIN NULL; END;", conn)
			stmt.containOutputPars = true
			stmt.Pars = []ParameterInfo{{DataType: NCHAR, Direction: Output, MaxLen: 131068, MaxCharLen: 32767, CharsetID: 873, Value: &first}}
			if tc.hasTail {
				stmt.Pars = append(stmt.Pars, ParameterInfo{DataType: NCHAR, Direction: tc.tail, MaxLen: 256, MaxCharLen: 64, CharsetID: 873, Value: &second})
			}
			var response bytes.Buffer
			response.WriteByte(7)
			writer := network.NewSessionWithInputBufferForDebug(nil)
			writer.WriteClr(&response, []byte("value"))
			response.Write([]byte{0, 9, 0x55})
			conn.session = network.NewSessionWithInputBufferForDebug(response.Bytes())
			err := stmt.read(&ResultSet{})
			if tc.wantError {
				if err == nil || !conn.bad || first != "previous-first" || second != "previous-second" {
					t.Fatalf("incomplete output admitted err=%v bad=%v destinations=%q/%q", err, conn.bad, first, second)
				}
			} else {
				if err != nil || conn.bad || stmt.Pars[0].oPrimValue != "value" {
					t.Fatalf("complete output rejected: %v bad=%v", err, conn.bad)
				}
				next, err := conn.session.GetByte()
				if err != nil || next != 0x55 {
					t.Fatalf("next message consumed: %x %v", next, err)
				}
			}
		})
	}
}
func TestLegacyCharacterArrayKeepsSingleRowFraming(t *testing.T) {
	conn := legacyOutputConnection(t)
	stmt := NewStmt("BEGIN NULL; END;", conn)
	stmt.containOutputPars = true
	stmt.Pars = []ParameterInfo{
		{DataType: NCHAR, Direction: Output, MaxLen: 131068, MaxCharLen: 32767, CharsetID: 873, MaxNoOfArrayElements: 1},
		{DataType: NCHAR, Direction: Output, MaxLen: 256, MaxCharLen: 64, CharsetID: 873},
	}
	var response bytes.Buffer
	response.WriteByte(7)
	response.Write([]byte{1, 1})
	writer := network.NewSessionWithInputBufferForDebug(nil)
	writer.WriteClr(&response, []byte("array-value"))
	response.WriteByte(0)
	writer.WriteClr(&response, []byte("next-value"))
	response.Write([]byte{0, 9, 0x55})
	conn.session = network.NewSessionWithInputBufferForDebug(response.Bytes())
	if err := stmt.read(&ResultSet{}); err != nil {
		t.Fatal(err)
	}
	values, ok := stmt.Pars[0].oPrimValue.([]ParameterInfo)
	if !ok || len(values) != 1 || values[0].oPrimValue != "array-value" || stmt.Pars[1].oPrimValue != "next-value" {
		t.Fatal("array/next output changed")
	}
	next, err := conn.session.GetByte()
	if err != nil || next != 0x55 {
		t.Fatalf("array framing consumed next message: %x %v", next, err)
	}
}
func TestLegacyPLSQLArrayInputRetainsItsExistingType(t *testing.T) {
	conn := legacyOutputConnection(t)
	stmt := NewStmt("DECLARE v VARCHAR2(32767):=:1; BEGIN NULL; END;", conn)
	par, err := stmt.NewParam("values", []string{strings.Repeat("x", 5000)}, 0, Input)
	if err != nil {
		t.Fatal(err)
	}
	if par.MaxNoOfArrayElements != 1 || par.DataType != NCHAR {
		t.Fatalf("array changed: elements=%d type=%v", par.MaxNoOfArrayElements, par.DataType)
	}
}

func TestPLSQLScalarCharacterBindPreservesVARCHAR2(t *testing.T) {
	const plsql = "DECLARE v VARCHAR2(32767) := :1; BEGIN NULL; END;"
	const sql = "SELECT :1 FROM DUAL"
	multibyte := strings.Repeat("é漢", 1000)
	for _, test := range []struct {
		name      string
		major     int
		minor     int
		sqlLimit  int
		statement string
		direction ParameterDirection
		input     string
		want      TNSType
	}{
		{"9i compatibility", 9, 2, 4000, plsql, Input, multibyte, NCHAR},
		{"10g scalar", 10, 2, 4000, plsql, Input, multibyte, NCHAR},
		{"11g scalar", 11, 2, 4000, plsql, Input, multibyte, NCHAR},
		{"12c scalar", 12, 1, 4000, plsql, Input, multibyte, NCHAR},
		{"small input", 12, 1, 4000, plsql, Input, strings.Repeat("a", 4000), NCHAR},
		{"first byte above SQL limit", 12, 1, 4000, plsql, Input, strings.Repeat("a", 4001), NCHAR},
		{"maximum PL/SQL length", 12, 1, 4000, plsql, Input, strings.Repeat("é漢", 6553) + "aa", NCHAR},
		{"above PL/SQL limit", 12, 1, 4000, plsql, Input, strings.Repeat("é漢", 6553) + "aaa", LongVarChar},
		{"IN OUT bind retains existing type", 12, 1, 4000, plsql, InOut, multibyte, LongVarChar},
		{"ordinary SQL on 9i", 9, 2, 4000, sql, Input, multibyte, LongVarChar},
		{"ordinary SQL on 12.1", 12, 1, 4000, sql, Input, multibyte, LongVarChar},
		{"12.2 PL/SQL retains negotiated type", 12, 2, 4000, plsql, Input, multibyte, LongVarChar},
		{"future PL/SQL retains negotiated type", 75, 0, 4000, plsql, Input, multibyte, LongVarChar},
		{"unknown version retains negotiated type", 0, 0, 4000, plsql, Input, multibyte, LongVarChar},
		{"extended SQL capability", 23, 0, 32767, sql, Input, multibyte, NCHAR},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := legacyOutputConnection(t)
			conn.dBVersion = &DBVersion{Number: uint16(test.major*1000 + test.minor*100), MajorVersion: test.major, MinorVersion: test.minor}
			conn.maxLen.varchar = test.sqlLimit
			stmt := NewStmt(test.statement, conn)
			par, err := stmt.NewParam("text", test.input, 0, test.direction)
			if err != nil {
				t.Fatal(err)
			}
			if par.DataType != test.want {
				t.Fatalf("type=%v, want %v", par.DataType, test.want)
			}
			if par.MaxLen != len(test.input) || !bytes.Equal(par.BValue, []byte(test.input)) {
				t.Fatal("character bind length or encoded bytes changed")
			}
		})
	}
}

func TestPLSQLScalarCharacterBindUsesEncodedLength(t *testing.T) {
	conn, err := NewConnection("", &configurations.ConnectionConfig{CharsetID: 178})
	if err != nil {
		t.Fatal(err)
	}
	conn.State = Opened
	conn.sStrConv = conn.cStrConv
	conn.maxLen.varchar = 4000
	conn.dBVersion = &DBVersion{Number: 11204, MajorVersion: 11, MinorVersion: 2}

	// WE8MSWIN1252 encodes each é as one byte, versus two bytes in Go.
	const count = 16384
	stmt := NewStmt("DECLARE v VARCHAR2(32767) := :1; BEGIN NULL; END;", conn)
	par, err := stmt.NewParam("text", strings.Repeat("é", count), 0, Input)
	if err != nil {
		t.Fatal(err)
	}
	if par.DataType != NCHAR || par.MaxLen != count || !bytes.Equal(par.BValue, bytes.Repeat([]byte{0xe9}, count)) {
		t.Fatalf("type=%v, max length=%d, encoded length=%d", par.DataType, par.MaxLen, len(par.BValue))
	}
}
