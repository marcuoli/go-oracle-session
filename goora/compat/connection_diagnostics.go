package compat

import (
	"context"
	"database/sql/driver"
	"time"
)

// ConnectionAttempt records one backend invocation in a diagnostic connection
// operation. It carries timing and observed protocol state without native
// driver objects, connection strings or error text.
type ConnectionAttempt struct {
	Mode      Mode
	Elapsed   time.Duration
	Connected bool
	// Handshake is absent when the backend does not provide native stages.
	Handshake *HandshakeDiagnostics
}

// HandshakeDiagnostics is the retained driver's value-only observation of
// connection setup. Phase and algorithm names are fixed driver identifiers.
// Each Known flag distinguishes a value from an unobserved field.
type HandshakeDiagnostics struct {
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

// ConnectWithDiagnostics makes one Connect operation and additionally returns
// the attempted backends in order, including a clean legacy selection when
// configured. The returned connection and error follow Connect's existing
// contract. Results are owned by this call and can be kept after it returns.
func (c *Connector) ConnectWithDiagnostics(ctx context.Context) (driver.Conn, []ConnectionAttempt, error) {
	var attempts []ConnectionAttempt
	conn, err := c.connect(ctx, &attempts)
	return conn, attempts, err
}

func connectAttempt(ctx context.Context, backend driver.Connector, mode Mode, attempts *[]ConnectionAttempt) (driver.Conn, error) {
	if attempts == nil {
		return backend.Connect(ctx)
	}
	start := time.Now()
	var conn driver.Conn
	var err error
	observation := ConnectionAttempt{Mode: mode}
	if detailed, ok := backend.(interface {
		connect(context.Context, *HandshakeDiagnostics) (driver.Conn, error)
	}); ok {
		observation.Handshake = &HandshakeDiagnostics{}
		conn, err = detailed.connect(ctx, observation.Handshake)
	} else {
		conn, err = backend.Connect(ctx)
	}
	observation.Connected = conn != nil && err == nil
	observation.Elapsed = time.Since(start)
	*attempts = append(*attempts, observation)
	return conn, err
}
