// Package compat provides opt-in, module-owned pure-Go Oracle driver compatibility.
// It does not change existing driver registrations or select another backend
// after authentication, transport, SQL or panic failures.
package compat

import (
	"context"
	"database/sql/driver"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/marcuoli/go-oracle-session/goora"
)

// Mode selects a connection policy before authentication.
type Mode uint8

const (
	// V2 selects the retained legacy-compatible backend without trying v3.
	// It is the zero/default policy, not automatic server-version detection.
	V2 Mode = iota
	// V3 selects the retained modern backend without trying v2.
	V3
	// Negotiated starts v3 and selects v2 only on its clean, pre-authentication
	// legacy-capability signal. Native qualification is configuration specific.
	Negotiated
)

// String returns the configured policy name.
func (m Mode) String() string {
	switch m {
	case V2:
		return "v2"
	case V3:
		return "v3"
	case Negotiated:
		return "negotiated"
	default:
		return "invalid"
	}
}

// Dialer supplies caller-owned network admission and transport lifetime control.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Options configures a connector before concurrent use. Dialer and warning
// callbacks must support concurrent physical admissions. The callback receives
// driver warning text synchronously; callers own redaction and collection.
type Options struct {
	Mode                Mode
	Dialer              Dialer
	LoginWarningHandler func(string) bool
}

var (
	// ErrInvalidOptions identifies an unknown compatibility policy.
	ErrInvalidOptions = errors.New("invalid Oracle compatibility options")
	// ErrInvalidDSN identifies malformed URL/query syntax without exposing its data.
	ErrInvalidDSN = errors.New("invalid Oracle compatibility URL")
	// ErrUnsupported identifies an unavailable native capability.
	ErrUnsupported = errors.New("unsupported Oracle compatibility capability")
	// ErrNoConnection identifies an invalid nil connection returned without error.
	ErrNoConnection = errors.New("Oracle driver returned no connection")
	// ErrLOBInUse requires consuming/freeing a LOB before rebinding or replacing it.
	ErrLOBInUse = errors.New("Oracle LOB still in use")
	// ErrLOBUnread requires materializing a lazy locator before obtaining Value.
	ErrLOBUnread = errors.New("Oracle LOB has not been read")
	// ErrLOBClosed identifies access after Free; SetValue starts a new value.
	ErrLOBClosed = errors.New("Oracle LOB is closed")
)

func prepareDSN(dsn string, mode Mode) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", ErrInvalidDSN
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", ErrInvalidDSN
	}
	if mode == V3 {
		if err := goora.PrepareURL(parsed); err != nil {
			return "", ErrInvalidDSN
		}
	} else {
		// FAST LOGIN is not a recognized v2 option. Consume only that reserved policy
		// key; all unrelated values, endpoint and credential bytes remain unchanged.
		for key := range query {
			if strings.ToUpper(key) == "FAST LOGIN" {
				delete(query, key)
			}
		}
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

// ConnectionMode reports the backend actually selected for a compatibility
// connection, such as the driver.Conn received inside sql.Conn.Raw.
func ConnectionMode(connection driver.Conn) (Mode, bool) {
	if c, ok := connection.(interface{ CompatibilityMode() Mode }); ok {
		return c.CompatibilityMode(), true
	}
	return V2, false
}
