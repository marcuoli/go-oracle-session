package advanced_nego

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/trace"
)

func TestInitialClientCryptoRejectionSignal(t *testing.T) {
	for _, tc := range []struct {
		name           string
		code           uint32
		unknownService bool
		configure      func(*configurations.ConnectionConfig)
		want           bool
	}{
		{name: "native crypto rejection", code: 12269, want: true},
		{name: "different rejection", code: 12660},
		{name: "unrecognized service", code: 12269, unknownService: true},
		{name: "external auth", code: 12269, configure: func(c *configurations.ConnectionConfig) { c.AuthType = configurations.OS }},
		{name: "wallet", code: 12269, configure: func(c *configurations.ConnectionConfig) { c.Wallet = &configurations.Wallet{} }},
		{name: "TLS client certificate", code: 12269, configure: func(c *configurations.ConnectionConfig) {
			c.TLSConfig = &tls.Config{Certificates: []tls.Certificate{{}}}
		}},
		{name: "TLS trust only", code: 12269, configure: func(c *configurations.ConnectionConfig) {
			c.TLSConfig = &tls.Config{ServerName: "fixture.invalid"}
		}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const headerSize = 13
			var response [headerSize + 8]byte
			binary.BigEndian.PutUint32(response[:4], 0xDEADBEEF)
			binary.BigEndian.PutUint16(response[4:6], uint16(len(response)))
			binary.BigEndian.PutUint32(response[6:10], uint32(version))
			binary.BigEndian.PutUint16(response[10:12], 1)
			service := uint16(2)
			if tc.unknownService {
				service = 5
			}
			binary.BigEndian.PutUint16(response[headerSize:headerSize+2], service)
			binary.BigEndian.PutUint32(response[headerSize+4:], tc.code)
			cfg, err := configurations.ParseConfig("oracle://fixture:unused@fixture.invalid:1521/service")
			if err != nil {
				t.Fatal(err)
			}
			if tc.configure != nil {
				tc.configure(cfg)
			}
			nego, err := NewAdvNego(network.NewSessionWithInputBufferForDebug(response[:]), trace.NilTracer(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err = nego.Read(); err == nil || errors.Is(err, ErrClientCryptoVersion) != tc.want {
				t.Fatalf("read error=%v, want switching eligibility %t", err, tc.want)
			}
		})
	}
}
