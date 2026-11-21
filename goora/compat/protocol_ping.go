package compat

import (
	"context"
	"database/sql/driver"
)

// PingProtocol probes an exclusively owned physical connection created by
// this package using its native Oracle Net ping. It returns ErrUnsupported for
// foreign connections or when the backend lacks a SQL-free ping. It never
// opens a connection or executes SQL. If the native probe fails or is canceled,
// discard the connection. A native break can still be settling when Ping
// returns. This function adds no independent transport deadline or disposal.
// A successful ping alone does not establish transaction usability or
// authorize session reuse.
func PingProtocol(ctx context.Context, connection driver.Conn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch native := connection.(type) {
	case *legacyDriverConnection:
		if native == nil || !native.Connection.SupportsProtocolPing() {
			return ErrUnsupported
		}
	case *modernDriverConnection:
		if native == nil || native.Connection == nil {
			return ErrUnsupported
		}
	default:
		return ErrUnsupported
	}

	validator := connection.(driver.Validator)
	if !validator.IsValid() {
		return driver.ErrBadConn
	}
	if err := connection.(driver.Pinger).Ping(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validator.IsValid() {
		return driver.ErrBadConn
	}
	return nil
}
