package go_ora

import (
	"errors"
	"testing"
)

func TestLegacyDetectionRequiresNegotiatedEvidence(t *testing.T) {
	for _, tt := range []struct {
		name   string
		caps   []byte
		legacy bool
	}{
		{"missing", nil, false}, {"short", make([]byte, 4), false},
		{"legacy", make([]byte, 8), true},
		{"modern", []byte{0, 0, 0, 0, 8, 0, 0, 0}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn := &Connection{detectLegacyServer: true, tcpNego: &TCPNego{ServerCompileTimeCaps: tt.caps}}
			if got := errors.Is(conn.checkLegacyServer(), ErrLegacyServer); got != tt.legacy {
				t.Fatalf("legacy=%v", got)
			}
			conn.detectLegacyServer = false
			if err := conn.checkLegacyServer(); err != nil {
				t.Fatal("opt-in detection affected ordinary connector")
			}
		})
	}
}
