// Package goora provides opt-in URL policy for caller-owned go-ora connectors.
package goora

import (
	"errors"
	"net/url"
	"strings"
)

// ErrInvalidURL identifies a nil URL or malformed query. It contains no URL data.
var ErrInvalidURL = errors.New("invalid go-ora URL query")

// PrepareURL disables fast login in a caller-owned go-ora URL. It removes all
// FAST LOGIN names recognized by the driver and sets one canonical FALSE value.
// Other option values, including duplicates, and non-query URL fields remain
// unchanged. The query is serialized with url.Values.Encode, normalizing its
// key order and escaping.
//
// Full login avoids reusing cached authentication state across physical
// connections; see https://github.com/sijms/go-ora/issues/732. The caller still
// qualifies its exact driver/server combination and owns all other tuning.
//
// PrepareURL changes only RawQuery after successful query validation. It returns
// ErrInvalidURL without mutation on failure and does not validate endpoints,
// open connections or wrap a driver. The caller must synchronize shared URL use.
func PrepareURL(parsed *url.URL) error {
	if parsed == nil {
		return ErrInvalidURL
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return ErrInvalidURL
	}
	// Match the driver's normalization so alternate names cannot compete with
	// the canonical value when the driver iterates its options map.
	for key := range query {
		if strings.ToUpper(key) == "FAST LOGIN" {
			delete(query, key)
		}
	}
	query.Set("FAST LOGIN", "FALSE")
	parsed.RawQuery = query.Encode()
	return nil
}
