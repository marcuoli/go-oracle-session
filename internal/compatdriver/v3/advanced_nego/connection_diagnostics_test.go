package advanced_nego

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/trace"
)

func TestConnectionDiagnosticsDistinguishUnknownAndNoNativeSecurity(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		encryptionID, integrityID byte
		encryption, integrity     string
	}{
		{"none", 0, 0, "none", "none"},
		{"aes_sha", 17, 5, "AES256", "SHA256"},
		{"legacy", 10, 1, "RC4_128", "MD5"},
		{"unrecognized", 255, 255, "unknown", "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const version = uint32(0x0C100200)
			input := append(diagnosticService(version, tt.encryptionID), diagnosticService(version, tt.integrityID)...)
			session := diagnosticSession(input)
			nego, err := NewAdvNego(session, trace.NilTracer(), &configurations.ConnectionConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if got := nego.ConnectionDiagnostics(); got != (ConnectionDiagnostics{}) {
				t.Fatalf("client proposals exposed as server observations: %+v", got)
			}
			if err := nego.serviceList[2].readServiceData(2); err != nil {
				t.Fatal(err)
			}
			if err := nego.serviceList[3].readServiceData(2); err != nil {
				t.Fatal(err)
			}
			got := nego.ConnectionDiagnostics()
			if !got.Encryption.VersionKnown || got.Encryption.Version != version || !got.Encryption.AlgorithmKnown || got.Encryption.Algorithm != tt.encryption {
				t.Fatalf("encryption = %+v", got.Encryption)
			}
			if !got.Integrity.VersionKnown || got.Integrity.Version != version || !got.Integrity.AlgorithmKnown || got.Integrity.Algorithm != tt.integrity {
				t.Fatalf("integrity = %+v", got.Integrity)
			}
		})
	}
}

func TestConnectionDiagnosticsKeepPartialNativeResponse(t *testing.T) {
	input := diagnosticService(0x0C100200, 17)
	input[11] = 4 // A mismatched algorithm-field type fails after the version was read.
	session := diagnosticSession(input)
	nego, err := NewAdvNego(session, trace.NilTracer(), &configurations.ConnectionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := nego.serviceList[2].readServiceData(2); err == nil {
		t.Fatal("malformed fixture accepted")
	}
	got := nego.ConnectionDiagnostics()
	if !got.Encryption.VersionKnown || got.Encryption.Version != 0x0C100200 || got.Encryption.AlgorithmKnown || got.Encryption.Algorithm != "" || got.Integrity.VersionKnown {
		t.Fatalf("partial response = %+v", got)
	}
}

func diagnosticService(version uint32, algorithm byte) []byte {
	input := []byte{0, 4, 0, 5, 0, 0, 0, 0, 0, 1, 0, 2, algorithm}
	binary.BigEndian.PutUint32(input[4:], version)
	return input
}

func diagnosticSession(input []byte) *network.Session {
	session := network.NewSession(&configurations.ConnectionConfig{}, trace.NilTracer())
	session.SaveState(&network.SessionState{InBuffer: bytes.NewBuffer(input), OutBuffer: &bytes.Buffer{}})
	return session
}
