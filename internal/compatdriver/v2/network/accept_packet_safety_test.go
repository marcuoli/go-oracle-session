package network

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/trace"
)

func TestAcceptPacketSafetyThroughReader(t *testing.T) {
	packet := func(size int, version, offset uint16) []byte {
		data := make([]byte, size)
		binary.BigEndian.PutUint16(data, uint16(size))
		data[4] = byte(ACCEPT)
		if size >= 32 {
			binary.BigEndian.PutUint16(data[8:], version)
			binary.BigEndian.PutUint16(data[20:], offset)
		}
		return data
	}
	overflow := packet(32, 312, 32)
	binary.BigEndian.PutUint16(overflow[28:], 65530)
	binary.BigEndian.PutUint16(overflow[30:], 10)
	badPayload := packet(32, 312, 32)
	binary.BigEndian.PutUint16(badPayload[18:], 1)
	reconnect := packet(36, 312, 36)
	binary.BigEndian.PutUint16(reconnect[28:], 32)
	binary.BigEndian.PutUint16(reconnect[30:], 4)
	copy(reconnect[32:], "host")
	for _, tt := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"legacy 312", packet(32, 312, 32), true},
		{"legacy 314", packet(32, 314, 32), true},
		{"modern 315", packet(40, 315, 40), true},
		{"modern 319", packet(61, 319, 61), true},
		{"reconnect at end", reconnect, true},
		{"short header", packet(8, 0, 0), false},
		{"short modern fields", packet(32, 315, 32), false},
		{"offset beyond packet", packet(40, 312, 100), false},
		{"offset inside header", packet(32, 312, 31), false},
		{"reconnect offset overflow", overflow, false},
		{"payload length mismatch", badPayload, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("ACCEPT packet reader panicked: %v", p)
				}
			}()
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			_ = server.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() {
				defer server.Close()
				_, err := server.Write(tt.data)
				if err == nil {
					_, err = io.Copy(io.Discard, server)
				}
				done <- err
			}()
			session := NewSession(&configurations.ConnectionConfig{}, trace.NilTracer())
			session.conn = client
			session.reader = bufio.NewReader(client)
			t.Cleanup(func() {
				session.Disconnect()
				if err := <-done; err != nil {
					t.Errorf("ACCEPT fixture: %v", err)
				}
			})
			got, err := session.readPacket()
			if !tt.valid {
				if got != nil || err == nil || !strings.Contains(err.Error(), "invalid TNS ACCEPT") {
					t.Fatalf("malformed ACCEPT returned %T, %v", got, err)
				}
				return
			}
			accepted, ok := got.(*AcceptPacket)
			if err != nil || !ok || accepted == nil {
				t.Fatalf("valid ACCEPT returned %T, %v", got, err)
			}
			if accepted.sessionCtx.Version != binary.BigEndian.Uint16(tt.data[8:]) {
				t.Fatal("valid protocol version changed")
			}
			if tt.name == "reconnect at end" && accepted.sessionCtx.ReconAddr != "host" {
				t.Fatal("valid reconnect address was lost")
			}
		})
	}
}
