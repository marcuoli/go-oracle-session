package go_ora

import (
	"crypto/tls"
	"errors"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
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
			conn := &Connection{connOption: &configurations.ConnectionConfig{}, detectLegacyServer: true, tcpNego: &TCPNego{ServerCompileTimeCaps: tt.caps}}
			if got := errors.Is(conn.checkLegacyServer(""), ErrLegacyServer); got != tt.legacy {
				t.Fatalf("legacy=%v", got)
			}
			conn.detectLegacyServer = false
			if err := conn.checkLegacyServer(""); err != nil {
				t.Fatal("opt-in detection affected ordinary connector")
			}
		})
	}
}

func TestLegacyDetectionStaysOnOriginalDriverWhenAuthenticationMayHaveStarted(t *testing.T) {
	conn := &Connection{
		connOption:         &configurations.ConnectionConfig{},
		detectLegacyServer: true,
		tcpNego:            &TCPNego{ServerCompileTimeCaps: make([]byte, 8)},
	}
	for _, service := range []string{"NTS", "KERBEROS5", "TCPS"} {
		if err := conn.checkLegacyServer(service); err != nil {
			t.Fatalf("negotiated %s authentication: %v", service, err)
		}
	}
	conn.connOption.TLSConfig = &tls.Config{Certificates: []tls.Certificate{{}}}
	if err := conn.checkLegacyServer(""); err != nil {
		t.Fatalf("client certificate: %v", err)
	}
	conn.connOption.TLSConfig = &tls.Config{ServerName: "fixture.invalid"}
	if !errors.Is(conn.checkLegacyServer(""), ErrLegacyServer) {
		t.Fatal("server trust policy alone blocked legacy detection")
	}
}
