package advanced_nego

import (
	"bytes"
	"errors"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
)

func TestStrictCryptoPreparationAndFolding(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		auth                             string
		version                          uint32
		encryption, integrity, keyLength int
		wantOK                           bool
	}{
		{"23ai service", "", 0x17000000, 17, 5, 64, true},
		{"26ai service", "", 0x171A4000, 17, 5, 64, true},
		{"upgraded service word on older Oracle", "", 0x0c101200, 17, 5, 64, true},
		{"legacy service word", "", 0x0c100200, 17, 5, 64, false},
		{"weak encryption", "", 0x17000000, 6, 5, 64, false},
		{"weak integrity", "", 0x17000000, 17, 3, 64, false},
		{"no services", "", 0x17000000, 0, 0, 64, false},
		{"external authentication", "NTS", 0x17000000, 17, 5, 64, false},
		{"missing DH material", "", 0x17000000, 17, 5, 63, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := network.NewSessionWithInputBufferForDebug(nil)
			nego, err := NewAdvNego(session, trace.NilTracer(), &configurations.ConnectionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			nego.RequireModernCrypto()
			encryption := nego.serviceList[2].(*encryptService)
			encryption.algoID, encryption.version, encryption.algorithmReceived = tc.encryption, tc.version, true
			integrity := nego.serviceList[3].(*dataIntegrityService)
			integrity.algoID, integrity.version, integrity.algorithmReceived = tc.integrity, tc.version, true
			integrity.sharedKey = bytes.Repeat([]byte{0x7c}, tc.keyLength)
			integrity.iV = []byte{0x12}
			err = nego.prepareModernCrypto(tc.auth)
			if !tc.wantOK {
				if !errors.Is(err, ErrStrictCryptoUnavailable) || integrity.modernIV != nil {
					t.Fatalf("expected terminal refusal, error=%v", err)
				}
				return
			}
			if err != nil || !bytes.Equal(encryption.modernIV, integrity.sharedKey[32:48]) {
				t.Fatalf("upgraded IV was not derived from shared material: %v", err)
			}
			if err := nego.StartServices(); err != nil {
				t.Fatal(err)
			}
			advanced := &session.Context.AdvancedService
			before := advanced.CryptAlgo
			if advanced.KeyFolded || before == nil || advanced.HashAlgo == nil {
				t.Fatal("pre-authentication services were not installed")
			}
			if err := nego.FoldKey([]byte{0x02}); err != nil {
				t.Fatal(err)
			}
			if !advanced.KeyFolded || advanced.CryptAlgo == before || integrity.sharedKey[0] != 0x7e || encryption.modernIV[0] != 0x7e || integrity.iV[0] != 0x12 {
				t.Fatal("fold did not re-key the active services from the password key")
			}
		})
	}
}
