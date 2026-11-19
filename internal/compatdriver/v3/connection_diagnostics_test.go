package go_ora

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every transport is an in-memory script; no socket or Oracle server is used.
func TestConnectionDiagnosticsIdentifyEOFPhaseWithoutChangingFailure(t *testing.T) {
	for _, tt := range []struct {
		name, phase string
		input       []byte
		tns, native bool
	}{
		{"before_accept", "tns_connect", nil, false, false},
		{"native_read", "native_negotiation_read", diagnosticAccept(true), true, true},
		{"ttc_protocol", "ttc_protocol", diagnosticAccept(false), true, false},
		{"ttc_datatypes", "ttc_datatypes", append(diagnosticAccept(false), diagnosticTTC()...), true, false},
		{"auth_challenge", "auth_challenge_read", append(append(diagnosticAccept(false), diagnosticTTC()...), diagnosticData([]byte{2, 0})...), true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plain, plainTransport := diagnosticConnector(tt.input)
			_, plainErr := plain.Connect(context.Background())
			connector, transport := diagnosticConnector(tt.input)
			conn, got, err := connector.ConnectWithDiagnostics(context.Background())
			if conn != nil || err != io.EOF || plainErr != io.EOF {
				t.Fatalf("connect = %v, %v; ordinary error = %v", conn, err, plainErr)
			}
			if got.Phase != tt.phase || got.PhaseDuration < 0 {
				t.Fatalf("phase = %q (%s), want %q", got.Phase, got.PhaseDuration, tt.phase)
			}
			if got.TNSVersionKnown != tt.tns || (tt.tns && got.TNSVersion != 312) || got.NativeNegotiationAttempted != tt.native {
				t.Fatalf("negotiation = %+v", got)
			}
			if got.AuthenticationCompleted || got.NativeNegotiationCompleted || got.EncryptionAlgorithmKnown || got.IntegrityAlgorithmKnown {
				t.Fatalf("unobserved negotiation reported as known: %+v", got)
			}
			if !transport.closed || !plainTransport.closed || !bytes.Equal(transport.writes.Bytes(), plainTransport.writes.Bytes()) {
				t.Fatal("diagnostics changed protocol writes or failure cleanup")
			}
			if strings.Contains(fmt.Sprintf("%+v", got), "fixture-secret") {
				t.Fatal("diagnostics contain a credential")
			}
		})
	}
}

func TestConnectionDiagnosticsArePerCall(t *testing.T) {
	connector := NewConnector("oracle://fixture:fixture-secret@fixture:1521/service?FAST+LOGIN=DISABLE").(*OracleConnector)
	connector.Dialer(diagnosticDialerFunc(func() net.Conn { return &diagnosticTransport{Reader: bytes.NewReader(nil)} }))
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, got, err := connector.ConnectWithDiagnostics(context.Background())
			if err != io.EOF || got.Phase != "tns_connect" || got.TNSVersionKnown {
				t.Errorf("connect = %+v, %v", got, err)
			}
		}()
	}
	workers.Wait()
}

func TestConnectionDiagnosticsAuthenticationAndServerVersionBoundaries(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("authentication_completed_%v", completed), func(t *testing.T) {
			input := append(append(diagnosticAccept(false), diagnosticTTC()...), diagnosticData([]byte{2, 0})...)
			phase := "auth_response_read"
			if completed {
				// TTC end-of-call 9 completes authentication without password exchange.
				input = append(input, diagnosticData([]byte{9})...)
				phase = "server_version"
			}
			connector, transport := diagnosticConnector(input)
			connector.connectString = "oracle://fixture:1521/service?FAST+LOGIN=DISABLE"
			_, got, err := connector.ConnectWithDiagnostics(context.Background())
			if err != io.EOF || got.Phase != phase || got.AuthenticationCompleted != completed || !got.TTCVersionKnown || got.TTCVersion != 8 || !transport.closed {
				t.Fatalf("connect = %+v, %v; closed=%v", got, err, transport.closed)
			}
		})
	}
}

func TestConnectionDiagnosticsResetForNextOpenAttempt(t *testing.T) {
	input := append(append(diagnosticAccept(false), diagnosticTTC()...), diagnosticData([]byte{2, 0})...)
	input = append(input, diagnosticData([]byte{9})...)
	conn, err := NewConnection("oracle://fixture:1521/service?FAST+LOGIN=DISABLE", nil)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &connectionDiagnosticState{}
	conn.connectionDiagnostics = diagnostics
	conn.connOption.Dialer = diagnosticDialerFunc(func() net.Conn {
		transport := &diagnosticTransport{Reader: bytes.NewReader(input)}
		input = nil // The second connection receives EOF before TNS ACCEPT.
		return transport
	})
	if err := conn.OpenWithContext(context.Background()); err != io.EOF {
		t.Fatalf("first open = %v", err)
	}
	conn.session.Disconnect()
	if !diagnostics.AuthenticationCompleted || !diagnostics.TNSVersionKnown || !diagnostics.TTCVersionKnown || diagnostics.Phase != "server_version" {
		t.Fatalf("first attempt did not reach expected fixture boundary: %+v", diagnostics.ConnectionDiagnostics)
	}
	if err := conn.OpenWithContext(context.Background()); err != io.EOF {
		t.Fatalf("second open = %v", err)
	}
	conn.session.Disconnect()
	got := diagnostics.ConnectionDiagnostics
	if got.Phase != "tns_connect" || got.TNSVersionKnown || got.TTCVersionKnown || got.AuthenticationCompleted || got.NativeNegotiationAttempted || got.NativeNegotiationCompleted || got.EncryptionAlgorithmKnown || got.IntegrityAlgorithmKnown {
		t.Fatalf("second attempt retained earlier observations: %+v", got)
	}
}

func diagnosticConnector(input []byte) (*OracleConnector, *diagnosticTransport) {
	transport := &diagnosticTransport{Reader: bytes.NewReader(input)}
	connector := NewConnector("oracle://fixture:fixture-secret@fixture:1521/service?FAST+LOGIN=DISABLE").(*OracleConnector)
	connector.Dialer(diagnosticDialerFunc(func() net.Conn { return transport }))
	return connector, transport
}

type diagnosticDialerFunc func() net.Conn

func (f diagnosticDialerFunc) DialContext(context.Context, string, string) (net.Conn, error) {
	return f(), nil
}

type diagnosticTransport struct {
	*bytes.Reader
	writes bytes.Buffer
	closed bool
}

func (c *diagnosticTransport) Write(p []byte) (int, error)      { return c.writes.Write(p) }
func (c *diagnosticTransport) Close() error                     { c.closed = true; return nil }
func (c *diagnosticTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (c *diagnosticTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (c *diagnosticTransport) SetDeadline(time.Time) error      { return nil }
func (c *diagnosticTransport) SetReadDeadline(time.Time) error  { return nil }
func (c *diagnosticTransport) SetWriteDeadline(time.Time) error { return nil }

func diagnosticAccept(native bool) []byte {
	packet := make([]byte, 32)
	binary.BigEndian.PutUint16(packet, uint16(len(packet)))
	packet[4] = 2
	binary.BigEndian.PutUint16(packet[8:], 312)
	binary.BigEndian.PutUint16(packet[12:], 8192)
	binary.BigEndian.PutUint16(packet[14:], 8192)
	binary.BigEndian.PutUint16(packet[20:], 32)
	if native {
		packet[22] = 1
	}
	return packet
}

func diagnosticData(payload []byte) []byte {
	packet := make([]byte, 10)
	binary.BigEndian.PutUint16(packet, uint16(len(packet)+len(payload)))
	packet[4] = 6
	return append(packet, payload...)
}

func diagnosticTTC() []byte {
	block := make([]byte, 11)
	binary.BigEndian.PutUint16(block[9:], 2000)
	payload := []byte{1, 6, 0, 'T', 0, 0x69, 0x03, 0, 0, 0, 0, byte(len(block))}
	payload = append(payload, block...)
	payload = append(payload, 8, 0, 0, 0, 0, 8, 0, 0, 8, 0)
	return diagnosticData(payload)
}
