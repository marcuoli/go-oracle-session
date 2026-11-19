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

func TestLegacyPLSQLVarcharInputDoesNotBecomeLong(t *testing.T) {
	text := strings.Repeat("é漢", 1000)
	for _, tc := range []struct {
		name, sql string
		major     int
		want      TNSType
	}{
		{"legacy PL/SQL", "BEGIN :1 := :1; END;", 9, NCHAR},
		{"legacy SQL", "SELECT :1 FROM DUAL", 9, LongVarChar},
		{"modern PL/SQL", "BEGIN :1 := :1; END;", 12, LongVarChar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := NewConnection("", &configurations.ConnectionConfig{CharsetID: 873})
			if err != nil {
				t.Fatal(err)
			}
			conn.State = Opened
			conn.sStrConv = conn.cStrConv
			conn.maxLen.varchar = 4000
			conn.dBVersion = &DBVersion{MajorVersion: tc.major}
			statement := NewStmt(tc.sql, conn)
			par, err := statement.NewParam("value", text, 0, Input)
			if err != nil || par.DataType != tc.want || !bytes.Equal(par.BValue, []byte(text)) {
				t.Fatalf("type=%v err=%v; want%v and exact bytes", par.DataType, err, tc.want)
			}
		})
	}
}
