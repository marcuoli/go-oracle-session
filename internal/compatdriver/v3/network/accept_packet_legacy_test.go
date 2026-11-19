package network

import (
	"encoding/binary"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	"testing"
)

func TestAcceptPacketLegacyAndModernLayouts(t *testing.T) {
	for _, tt := range []struct {
		version uint16
		size    int
	}{{312, 32}, {314, 32}, {315, 40}, {319, 61}} {
		data := make([]byte, tt.size)
		binary.BigEndian.PutUint16(data, uint16(tt.size))
		binary.BigEndian.PutUint16(data[8:], tt.version)
		binary.BigEndian.PutUint16(data[20:], uint16(tt.size))
		got := newAcceptPacketFromData(data, &configurations.ConnectionConfig{})
		if got == nil || got.sessionCtx.Version != tt.version {
			t.Fatalf("rejected layout %d/%d", tt.version, tt.size)
		}
	}
}

func TestAcceptPacketRejectsTruncatedOrInvalidOffsets(t *testing.T) {
	for size := 0; size < 32; size++ {
		if newAcceptPacketFromData(make([]byte, size), nil) != nil {
			t.Fatalf("accepted size %d", size)
		}
	}
	for _, tt := range []struct {
		size                    int
		version, offset, length uint16
	}{{32, 315, 32, 32}, {40, 312, 100, 40}, {32, 312, 32, 2}, {32, 312, 32, 100}} {
		data := make([]byte, tt.size)
		binary.BigEndian.PutUint16(data, tt.length)
		binary.BigEndian.PutUint16(data[8:], tt.version)
		binary.BigEndian.PutUint16(data[20:], tt.offset)
		if newAcceptPacketFromData(data, nil) != nil {
			t.Fatalf("accepted malformed layout %+v", tt)
		}
	}
}
