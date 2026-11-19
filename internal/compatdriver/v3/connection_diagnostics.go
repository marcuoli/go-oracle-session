package go_ora

import (
	"context"
	"database/sql/driver"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/advanced_nego"
)

// ConnectionDiagnostics is a value-only snapshot of connection setup. Names are
// fixed driver identifiers, never protocol strings. Unknown negotiation results
// remain explicitly unknown; no credentials, packet data, keys or IVs are kept.
type ConnectionDiagnostics struct {
	Phase                      string
	PhaseDuration              time.Duration
	TNSVersion                 uint16
	TNSVersionKnown            bool
	TTCVersion                 uint8
	TTCVersionKnown            bool
	NativeNegotiationAttempted bool
	NativeNegotiationCompleted bool
	EncryptionAlgorithm        string
	EncryptionAlgorithmKnown   bool
	EncryptionVersion          uint32
	EncryptionVersionKnown     bool
	IntegrityAlgorithm         string
	IntegrityAlgorithmKnown    bool
	IntegrityVersion           uint32
	IntegrityVersionKnown      bool
	AuthenticationCompleted    bool
}

type connectionDiagnosticState struct {
	ConnectionDiagnostics
	phaseStarted time.Time
}

func (d *connectionDiagnosticState) phase(name string) {
	if d == nil {
		return
	}
	d.Phase = name
	d.phaseStarted = time.Now()
}

func (d *connectionDiagnosticState) native(nego *advanced_nego.AdvNego) {
	if d == nil {
		return
	}
	security := nego.ConnectionDiagnostics()
	d.EncryptionAlgorithm = security.Encryption.Algorithm
	d.EncryptionAlgorithmKnown = security.Encryption.AlgorithmKnown
	d.EncryptionVersion = security.Encryption.Version
	d.EncryptionVersionKnown = security.Encryption.VersionKnown
	d.IntegrityAlgorithm = security.Integrity.Algorithm
	d.IntegrityAlgorithmKnown = security.Integrity.AlgorithmKnown
	d.IntegrityVersion = security.Integrity.Version
	d.IntegrityVersionKnown = security.Integrity.VersionKnown
}

// ConnectWithDiagnostics performs the same single Connect operation and returns
// its original error unchanged, with a private per-call diagnostic snapshot.
// It neither enables tracing nor changes the configured authentication flow.
func (connector *OracleConnector) ConnectWithDiagnostics(ctx context.Context) (driver.Conn, ConnectionDiagnostics, error) {
	diagnostics := &connectionDiagnosticState{}
	conn, err := connector.connect(ctx, diagnostics)
	diagnostics.PhaseDuration = time.Since(diagnostics.phaseStarted)
	return conn, diagnostics.ConnectionDiagnostics, err
}
