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

func TestStrictCryptoHonorsOfferedServices(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		encrypt, integrity, wantE, wantI int
		allowed                          bool
	}{
		{"required encryption missing", 3, 0, 0, 5, false},
		{"required integrity missing", 0, 3, 17, 0, false},
		{"rejected encryption negotiated", 1, 3, 17, 5, false},
		{"rejected integrity negotiated", 3, 1, 17, 5, false},
		{"encryption only", 3, 1, 17, 0, true},
		{"integrity only", 1, 3, 0, 5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &configurations.ConnectionConfig{}
			cfg.EncServiceLevel, cfg.IntServiceLevel = tc.encrypt, tc.integrity
			session := network.NewSessionWithInputBufferForDebug(nil)
			nego, err := NewAdvNego(session, trace.NilTracer(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			encryption := nego.serviceList[2].(*encryptService)
			encryption.algoID, encryption.algorithmReceived = tc.wantE, true
			integrity := nego.serviceList[3].(*dataIntegrityService)
			integrity.algoID, integrity.algorithmReceived = tc.wantI, true
			integrity.sharedKey = make([]byte, 64)
			err = nego.prepareModernCrypto("")
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrStrictCryptoUnavailable) {
				t.Fatalf("strict selection error=%v, allowed=%t", err, tc.allowed)
			}
		})
	}
}

func TestFirstV3SelectsNegotiatedModernCrypto(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		encVersion, intVersion  uint32
		encryption, integrity   int
		keyLength               int
		auth                    string
		allow                   bool
		require                 bool
		omitIntegrity           bool
		wantModern, wantRefusal bool
	}{
		{name: "plain Oracle 26ai TCP", encVersion: 0x171a4000, intVersion: 0x171a4000, allow: true},
		{name: "old AES peer", encVersion: 0x0c100200, intVersion: 0x0c100200, encryption: 17, integrity: 5, allow: true},
		{name: "old external login", encVersion: 0x0c100200, intVersion: 0x0c100200, encryption: 17, integrity: 3, auth: "NTS", allow: true},
		{name: "server selects external login", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, integrity: 5, auth: "NTS", allow: true},
		{name: "new AES256 with SHA256", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, integrity: 5, keyLength: 64, allow: true, wantModern: true},
		{name: "backported upgraded service", encVersion: 0x0c101200, intVersion: 0x0c101200, encryption: 17, integrity: 5, keyLength: 64, allow: true, wantModern: true},
		{name: "upgraded integrity only", encVersion: 0x171a4000, intVersion: 0x171a4000, integrity: 5, keyLength: 64, allow: true, wantModern: true},
		{name: "upgraded encryption only", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, keyLength: 64, allow: true, wantModern: true},
		{name: "unoffered mixed service", encVersion: 0x171a4000, intVersion: 0x0c100200, encryption: 17, integrity: 3, keyLength: 64, allow: true, wantRefusal: true},
		{name: "incomplete upgraded service response", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, keyLength: 64, allow: true, omitIntegrity: true, wantRefusal: true},
		{name: "missing upgraded key", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, integrity: 5, keyLength: 63, allow: true, wantRefusal: true},
		{name: "strict alternate refuses plain", encVersion: 0x171a4000, intVersion: 0x171a4000, require: true, wantRefusal: true},
		{name: "upgrade disabled for existing profiles", encVersion: 0x171a4000, intVersion: 0x171a4000, encryption: 17, integrity: 5, keyLength: 64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := network.NewSessionWithInputBufferForDebug(nil)
			nego, err := NewAdvNego(session, trace.NilTracer(), &configurations.ConnectionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.allow {
				nego.AllowModernCrypto()
			}
			if tc.require {
				nego.RequireModernCrypto()
			}
			encryption := nego.serviceList[2].(*encryptService)
			encryption.algoID, encryption.version, encryption.algorithmReceived = tc.encryption, tc.encVersion, true
			integrity := nego.serviceList[3].(*dataIntegrityService)
			integrity.algoID, integrity.version, integrity.algorithmReceived = tc.integrity, tc.intVersion, !tc.omitIntegrity
			integrity.sharedKey = bytes.Repeat([]byte{0x7c}, tc.keyLength)
			err = nego.prepareNegotiatedCrypto(tc.auth)
			if (tc.wantRefusal && !errors.Is(err, ErrStrictCryptoUnavailable)) || (!tc.wantRefusal && err != nil) {
				t.Fatalf("selection returned %v, want refusal = %v", err, tc.wantRefusal)
			}
			if got := nego.UsesModernCrypto(); got != tc.wantModern {
				t.Fatalf("upgraded exchange active = %v, want %v", got, tc.wantModern)
			}
		})
	}
}

func TestV3ModernCryptoRejectsDuplicateServiceRecords(t *testing.T) {
	// The response repeats one integrity record. A second record must never
	// change the first record's algorithm or reuse its negotiation state.
	response := []byte{
		0xde, 0xad, 0xbe, 0xef, 0, 55, 0x17, 0, 0, 0, 0, 2, 0,
		0, 3, 0, 2, 0, 0, 0, 0,
		0, 4, 0, 5, 0x17, 0, 0, 0,
		0, 1, 0, 2, 0,
		0, 3, 0, 2, 0, 0, 0, 0,
		0, 4, 0, 5, 0x17, 0, 0, 0,
		0, 1, 0, 2, 5,
	}
	for _, tc := range []struct {
		name   string
		enable func(*AdvNego)
	}{
		{"legacy", nil},
		{"initial V3", (*AdvNego).AllowModernCrypto},
		{"strict fallback", (*AdvNego).RequireModernCrypto},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := diagnosticSession(response)
			nego, err := NewAdvNego(session, trace.NilTracer(), &configurations.ConnectionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.enable != nil {
				tc.enable(nego)
			}
			err = nego.Read()
			integrity := nego.serviceList[3].(*dataIntegrityService)
			if tc.enable == nil {
				if err != nil || integrity.algoID != 5 {
					t.Fatalf("legacy parser selected %d, error = %v", integrity.algoID, err)
				}
				return
			}
			if !errors.Is(err, ErrStrictCryptoUnavailable) || integrity.algoID != 0 {
				t.Fatalf("expected refusal before decoding the duplicate, got algorithm %d, error = %v", integrity.algoID, err)
			}
		})
	}
}
