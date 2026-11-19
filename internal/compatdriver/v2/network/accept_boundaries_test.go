package network

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
)

// TestAcceptOptionalFieldBoundaries covers complete packets on either side of
// the optional four-byte field. These are synthetic layouts, not native logins.
func TestAcceptOptionalFieldBoundaries(t *testing.T) {
	packet := func(size int) []byte {
		data := make([]byte, size)
		binary.BigEndian.PutUint16(data, uint16(size))
		data[4] = byte(ACCEPT)
		version := uint16(315)
		if size < 40 {
			version = 312
		}
		binary.BigEndian.PutUint16(data[8:], version)
		binary.BigEndian.PutUint16(data[20:], uint16(size))
		if size >= 40 {
			binary.BigEndian.PutUint32(data[32:], 8192)
			binary.BigEndian.PutUint32(data[36:], 8192)
		}
		if size >= 45 {
			binary.BigEndian.PutUint32(data[41:], 0x12000000)
		}
		return data
	}
	for _, size := range []int{32, 40, 41, 44, 45, 61} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			got := newAcceptPacketFromData(packet(size), &configurations.ConnectionConfig{})
			if got == nil {
				t.Fatal("complete ACCEPT rejected")
			}
			if int(got.dataOffset) != size || int(got.length) != size {
				t.Fatal("packet identity changed")
			}

		})
	}
	for _, change := range []struct {
		name string
		edit func([]byte)
	}{
		{"declared too short", func(p []byte) { binary.BigEndian.PutUint16(p, 40) }},
		{"declared too long", func(p []byte) { binary.BigEndian.PutUint16(p, 44) }},
		{"offset before header", func(p []byte) { binary.BigEndian.PutUint16(p[20:], 31) }},
		{"offset beyond packet", func(p []byte) { binary.BigEndian.PutUint16(p[20:], 42) }},
		{"reconnect overflow", func(p []byte) { binary.BigEndian.PutUint16(p[28:], 65530); binary.BigEndian.PutUint16(p[30:], 10) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			p := packet(41)
			change.edit(p)
			if got := newAcceptPacketFromData(p, &configurations.ConnectionConfig{}); got != nil {
				t.Fatal("malformed ACCEPT accepted")
			}
		})
	}
}
