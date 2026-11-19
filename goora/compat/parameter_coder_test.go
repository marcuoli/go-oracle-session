package compat

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/converters"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/parameter_coder"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

func TestModernConnectorInitializationIsIsolatedAndConcurrent(t *testing.T) {
	t.Parallel()
	const connectorCount = 32
	connectors := make(chan *modernDriverConnector, connectorCount)
	for range connectorCount {
		go func() {
			// An invalid DSN proves registration does not open a connection while
			// creating and closing its temporary sql.DB handle.
			connector := mustParameterConnector(t)
			connectors <- connector.modern.(*modernDriverConnector)
		}()
	}
	seen := make(map[driver.Driver]bool, connectorCount)
	for range connectorCount {
		connector := <-connectors
		if connector.setupErr != nil {
			t.Errorf("initialize isolated parameter coders: %v", connector.setupErr)
		}
		drv := connector.Driver()
		if drv == modern.GetDefaultDriver() || seen[drv] {
			t.Error("connector shares a parameter-coder registry with another driver")
		}
		seen[drv] = true
	}
}

func TestModernConnectorSetupFailureStopsBeforeConnection(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("parameter-coder setup failed")
	// A nil underlying connector must never be reached after setup fails.
	modernConnector := &modernDriverConnector{setupErr: wantErr}
	legacyCalls := 0
	connector := &Connector{mode: Negotiated,
		modern: modernConnector,
		legacy: &testBackend{connect: func(context.Context) (driver.Conn, error) {
			legacyCalls++
			return nil, nil
		}},
	}
	connection, err := connector.Connect(t.Context())
	if connection != nil || !errors.Is(err, wantErr) || legacyCalls != 0 {
		t.Fatalf("setup failure result: connection = %v, error = %v, legacy calls = %d", connection, err, legacyCalls)
	}
}

func newTestCharacterParameter() parameter_coder.OracleParameterCoder {
	return &modernCharacterParameter{}
}

type characterCoderTestConnection struct{ modern.Connection }

func (*characterCoderTestConnection) GetStringCoder(int, int) (converters.IStringConverter, error) {
	return converters.NewStringConverter(873), nil
}

func TestModernCharacterNullPreservesFollowingDate(t *testing.T) {
	t.Parallel()
	// A described untyped NULL has no CLR even when its byte length is one.
	// The next bytes are the DATE value and the TTC end-of-response marker.
	date := []byte{120, 126, 9, 20, 13, 35, 57}
	for _, dataType := range []uint16{types.NCHAR, types.CHAR} {
		t.Run(fmt.Sprint(dataType), func(t *testing.T) {
			parameter := newTestCharacterParameter().Copy()
			parameter.SetParameterInfo(parameter_coder.BasicParameter{
				DataType: dataType, MaxLen: 1, MaxCharLen: 0, CharsetID: 873,
			})
			parameter.SetBytes([]byte("previous row"))
			wire := append([]byte{byte(len(date))}, date...)
			wire = append(wire, 9)
			session := network.NewMemorySession(wire, nil, network.SessionProperties{})
			if err := parameter.Read(session); err != nil {
				t.Fatal(err)
			}
			if parameter.Bytes() != nil {
				t.Fatalf("NULL consumed following column: %x", parameter.Bytes())
			}
			if value, err := parameter.Decode(&modern.Connection{}); err != nil || value != nil {
				t.Fatalf("decoded NULL = %v, error = %v", value, err)
			}
			following := parameter_coder.DateParameter{BasicParameter: parameter_coder.BasicParameter{DataType: types.DATE, MaxLen: 7}}
			if err := following.Read(session); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(following.Bytes(), date) {
				t.Fatalf("DATE bytes = %x, want %x", following.Bytes(), date)
			}
			if marker, err := session.GetByte(); err != nil || marker != 9 {
				t.Fatalf("TTC marker = %d, error = %v", marker, err)
			}
		})
	}
}

func TestModernDeclaredCharacterConsumesItsCLR(t *testing.T) {
	t.Parallel()
	for _, dataType := range []uint16{types.NCHAR, types.CHAR} {
		for _, value := range [][]byte{nil, []byte("A")} {
			t.Run(fmt.Sprintf("%d/%x", dataType, value), func(t *testing.T) {
				parameter := newTestCharacterParameter().Copy()
				parameter.SetParameterInfo(parameter_coder.BasicParameter{DataType: dataType, MaxLen: 4, MaxCharLen: 1})
				wire := append([]byte{byte(len(value))}, value...)
				wire = append(wire, 9)
				session := network.NewMemorySession(wire, nil, network.SessionProperties{})
				if err := parameter.Read(session); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(parameter.Bytes(), value) {
					t.Fatalf("character bytes = %x, want %x", parameter.Bytes(), value)
				}
				if marker, err := session.GetByte(); err != nil || marker != 9 {
					t.Fatalf("TTC marker = %d, error = %v", marker, err)
				}
			})
		}
	}
}

func TestModernCharacterEncodingPreservesDeclaredNullOutput(t *testing.T) {
	t.Parallel()
	parameter := newTestCharacterParameter().Copy()
	parameter.SetParameterInfo(parameter_coder.BasicParameter{CharsetID: 873})
	if err := parameter.Encode(sql.NullString{}, &characterCoderTestConnection{}); err != nil {
		t.Fatal(err)
	}
	info := parameter.GetParameterInfo()
	if info.DataType != types.NCHAR || info.MaxLen != 1 || info.MaxCharLen != 1 || parameter.Bytes() != nil {
		t.Fatalf("NULL output bind metadata changed: %+v", info)
	}
	session := network.NewMemorySession([]byte{0, 9}, nil, network.SessionProperties{})
	if err := parameter.Read(session); err != nil {
		t.Fatal(err)
	}
	if marker, err := session.GetByte(); err != nil || marker != 9 {
		t.Fatalf("NULL output bind did not consume CLR: marker = %d, error = %v", marker, err)
	}
}

func TestModernCharacterDelegatesUDTArrayAndLongReads(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		dataType uint16
		udt      bool
		array    bool
		wire     []byte
	}{
		{"UDT NCHAR", types.NCHAR, true, false, []byte{0xFE, 0, 0, 0, 1, 'A', 9}},
		{"UDT CHAR", types.CHAR, true, false, []byte{0xFE, 0, 0, 0, 1, 'A', 9}},
		{"array NCHAR", types.NCHAR, false, true, []byte{1, 'A', 9}},
		{"array CHAR", types.CHAR, false, true, []byte{1, 'A', 9}},
		{"LONG", types.LONG, false, false, []byte{1, 'A', 9}},
		{"LongVarChar", types.LongVarChar, false, false, []byte{1, 'A', 9}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parameter := newTestCharacterParameter().Copy()
			parameter.SetParameterInfo(parameter_coder.BasicParameter{DataType: tt.dataType, MaxLen: 4})
			if tt.udt {
				parameter.SetAsUDTPar()
			}
			if tt.array {
				parameter.SetAsArrayPar()
			}
			session := network.NewMemorySession(tt.wire, nil, network.SessionProperties{})
			if err := parameter.Read(session); err != nil {
				t.Fatal(err)
			}
			if string(parameter.Bytes()) != "A" {
				t.Fatalf("character bytes = %x, want A", parameter.Bytes())
			}
			if marker, err := session.GetByte(); err != nil || marker != 9 {
				t.Fatalf("TTC marker = %d, error = %v", marker, err)
			}
		})
	}
}

func TestModernCharacterEncodingPreservesUTF8(t *testing.T) {
	t.Parallel()
	parameter := newTestCharacterParameter().Copy()
	if err := parameter.Encode("é", &characterCoderTestConnection{}); err != nil {
		t.Fatal(err)
	}
	info := parameter.GetParameterInfo()
	if info.DataType != types.NCHAR || info.MaxLen != 2 || info.MaxCharLen != 2 || info.CharsetID != 873 {
		t.Fatalf("UTF-8 bind metadata changed: %+v", info)
	}
	session := network.NewMemorySession(nil, nil, network.SessionProperties{})
	if err := parameter.Write(session); err != nil {
		t.Fatal(err)
	}
	if wire := session.GetWriteBuffer(); !bytes.Equal(wire, []byte{2, 0xC3, 0xA9}) {
		t.Fatalf("UTF-8 wire bytes = %x", wire)
	}
}

func TestModernDeclaredCharacterReadReturnsTruncatedCLRError(t *testing.T) {
	t.Parallel()
	parameter := newTestCharacterParameter().Copy()
	parameter.SetParameterInfo(parameter_coder.BasicParameter{DataType: types.NCHAR, MaxLen: 4, MaxCharLen: 1})
	session := network.NewMemorySession([]byte{1}, nil, network.SessionProperties{})
	if err := parameter.Read(session); err == nil {
		t.Fatal("truncated CLR was accepted")
	}
}

func mustParameterConnector(t *testing.T) *Connector {
	t.Helper()
	c, err := NewConnector("oracle://fixture:unused@127.0.0.1:1/fixture", Options{Mode: Negotiated})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
