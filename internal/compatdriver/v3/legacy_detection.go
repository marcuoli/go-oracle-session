package go_ora

import "errors"

// ErrLegacyServer is returned only after successful protocol negotiation and
// before authentication when the server lacks modern logon capabilities.
// It must never be used for an authentication or transport failure.
var ErrLegacyServer = errors.New("server requires legacy Oracle authentication protocol")

// WithLegacyServerDetection lets an application select its legacy driver before
// this connector attempts authentication. Failed negotiation stays an error.
func (connector *OracleConnector) WithLegacyServerDetection() {
	connector.detectLegacyServer = true
}

func (conn *Connection) checkLegacyServer() error {
	if conn.detectLegacyServer && conn.tcpNego != nil && len(conn.tcpNego.ServerCompileTimeCaps) >= 8 && conn.tcpNego.ServerCompileTimeCaps[4]&8 == 0 {
		return ErrLegacyServer
	}
	return nil
}
