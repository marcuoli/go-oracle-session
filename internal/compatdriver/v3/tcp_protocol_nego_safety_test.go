package go_ora

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
)

func TestProtocolNegotiationNationalCharsetBounds(t *testing.T) {
	valid := make([]byte, 11)
	binary.BigEndian.PutUint16(valid[9:], 2000)
	overflow := make([]byte, 11)
	overflow[5] = 250
	binary.BigEndian.PutUint16(overflow[3:], 2000) // A wrapped byte offset would accept this.
	large := make([]byte, 266)
	large[5] = 255
	binary.BigEndian.PutUint16(large[264:], 2000)
	for _, tt := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"empty", nil, false},
		{"short header", make([]byte, 6), false},
		{"short charset", make([]byte, 10), false},
		{"out of range offset", []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 7, 208}, false},
		{"byte offset overflow", overflow, false},
		{"valid minimum", valid, true},
		{"valid large offset", large, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("protocol negotiation panicked: %v", p)
				}
			}()
			conn := nationalCharsetTestConnection(t, tt.data)
			err := conn.protocolNegotiation()
			if !tt.valid {
				if err == nil || !strings.Contains(err.Error(), "national character set") {
					t.Fatalf("malformed character set block returned %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if conn.tcpNego.ServerNCharset != 2000 {
				t.Fatalf("national character set = %d, want 2000", conn.tcpNego.ServerNCharset)
			}
		})
	}
}

// Exercise real packet and negotiation readers through an in-memory transport.
// No authentication request or database connection is made.
func nationalCharsetTestConnection(t *testing.T, block []byte) *Connection {
	t.Helper()
	client, server := net.Pipe()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		var readPacket func() error
		readPacket = func() error {
			header := make([]byte, 8)
			if _, err := io.ReadFull(server, header); err != nil {
				return err
			}
			length := int(binary.BigEndian.Uint16(header))
			if length < len(header) {
				return fmt.Errorf("invalid fixture request length %d", length)
			}
			body := make([]byte, length-len(header))
			_, err := io.ReadFull(server, body)
			if err == nil && header[4] == 1 && len(body) >= 20 && int(binary.BigEndian.Uint16(body[18:])) == length {
				return readPacket() // The connect descriptor follows in a DATA packet.
			}
			return err
		}
		if err := readPacket(); err != nil {
			done <- err
			return
		}
		accept := make([]byte, 32)
		binary.BigEndian.PutUint16(accept, uint16(len(accept)))
		accept[4] = 2
		binary.BigEndian.PutUint16(accept[8:], 312)
		binary.BigEndian.PutUint16(accept[12:], 8192)
		binary.BigEndian.PutUint16(accept[14:], 8192)
		binary.BigEndian.PutUint16(accept[20:], 32)
		if _, err := server.Write(accept); err != nil {
			done <- err
			return
		}
		if err := readPacket(); err != nil {
			done <- err
			return
		}
		// TTC version, text, AL32UTF8 charset, flags, empty charset list.
		payload := []byte{1, 6, 0, 'T', 0, 0x69, 0x03, 0, 0, 0}
		payload = append(payload, byte(len(block)>>8), byte(len(block)))
		payload = append(payload, block...)
		payload = append(payload, 8, 0, 0, 0, 0, 8, 0, 0, 0, 0)
		payload = append(payload, make([]byte, 64)...) // v2 reads ahead for its server-string limit.
		packet := make([]byte, 10)
		binary.BigEndian.PutUint16(packet, uint16(len(packet)+len(payload)))
		packet[4] = 6
		_, err := server.Write(append(packet, payload...))
		if err == nil {
			_, err = io.Copy(io.Discard, server)
		}
		done <- err
	}()
	conn, err := NewConnection("oracle://fixture:unused@fixture:1521/service", nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.connOption.Dialer = nationalCharsetTestDialer{client}
	conn.tracer = trace.NilTracer()
	conn.session = network.NewSession(conn.connOption, conn.tracer)
	t.Cleanup(func() {
		conn.session.Disconnect()
		if err := <-done; err != nil {
			t.Errorf("negotiation fixture: %v", err)
		}
	})
	if err := conn.session.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	return conn
}

type nationalCharsetTestDialer struct{ conn net.Conn }

func (d nationalCharsetTestDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}
