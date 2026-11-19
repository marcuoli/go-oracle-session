package go_ora

import "errors"

// Discard closes an uncertain physical transport without logoff or SQL. The
// owner must join active operation/cancellation work first. A discarded
// connection is terminal even when transport cleanup reports an error.
func (conn *Connection) Discard() error {
	if conn == nil || conn.State == Closed {
		return nil
	}
	conn.bad = true
	conn.State = Closed
	var transportErr, tracerErr error
	if conn.session != nil {
		transportErr = conn.session.DisconnectWithError()
	}
	if conn.tracer != nil {
		tracerErr = conn.tracer.Close()
	}
	return errors.Join(transportErr, tracerErr)
}
