package compat

import (
	"context"
	"database/sql/driver"
	"errors"
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

// ConnectionObserver brackets each backend invocation synchronously. Before is
// called immediately before admission. After receives the attempt snapshot and
// the original backend error, which may contain private data. The caller owns
// redaction. A callback error stops selection. A connection rejected by After
// is closed before returning, and any cleanup failure is retained. Callbacks
// shared across concurrent ConnectObserved calls must support concurrent use.
type ConnectionObserver struct {
	Before func(Mode) error
	After  func(ConnectionAttempt, error) error
}

// ConnectObserved uses the same selection and context as Connect. The observer
// can reject an attempt before admission or reject its outcome before another
// backend is selected or a connection is returned. An empty observer behaves
// like Connect.
func (c *Connector) ConnectObserved(ctx context.Context, observer ConnectionObserver) (driver.Conn, error) {
	return c.connect(ctx, observer, nil)
}

// ConnectWithDiagnostics makes one Connect operation and additionally returns
// the attempted backends in order, including a clean legacy selection when
// configured. The returned connection and error follow Connect's existing
// contract. Results are owned by this call and can be kept after it returns.
func (c *Connector) ConnectWithDiagnostics(ctx context.Context) (driver.Conn, []ConnectionAttempt, error) {
	var attempts []ConnectionAttempt
	conn, err := c.connect(ctx, ConnectionObserver{}, &attempts)
	return conn, attempts, err
}

func connectAttempt(ctx context.Context, backend driver.Connector, mode Mode, observer ConnectionObserver, attempts *[]ConnectionAttempt) (driver.Conn, error) {
	if observer.Before != nil {
		if err := observer.Before(mode); err != nil {
			return rejectObservation(nil, err, nil)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if observer.After == nil && attempts == nil {
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
	if attempts != nil {
		*attempts = append(*attempts, observation)
	}
	if observer.After != nil {
		// Admission remains owned here if the callback panics or consumes the
		// rest of the caller's deadline before the connection can be returned.
		recorded := false
		defer func() {
			if !recorded && conn != nil {
				_ = conn.Close()
			}
		}()
		observeErr := observer.After(observation, err)
		recorded = true
		if observeErr != nil {
			return rejectObservation(conn, observeErr, err)
		}
		if err == nil && conn != nil && ctx.Err() != nil {
			return finishConnection(conn, ctx.Err())
		}
	}
	return conn, err
}

func rejectObservation(conn driver.Conn, observeErr, backendErr error) (driver.Conn, error) {
	// A joined error cannot be mistaken for the clean capability signal.
	_, err := finishConnection(conn, errors.Join(observeErr, backendErr))
	if errors.Is(err, driver.ErrBadConn) {
		return nil, nonRetryableObservationError{err}
	}
	return nil, err
}

// A failed observation must not request another admission from database/sql.
// Keep meaningful typed causes available without exposing its retry sentinel.
type nonRetryableObservationError struct{ cause error }

func (e nonRetryableObservationError) Error() string { return e.cause.Error() }
func (e nonRetryableObservationError) Is(target error) bool {
	return target != driver.ErrBadConn && errors.Is(e.cause, target)
}
func (e nonRetryableObservationError) As(target any) bool { return errors.As(e.cause, target) }
