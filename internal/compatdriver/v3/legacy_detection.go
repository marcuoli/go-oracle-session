package go_ora

import (
	"errors"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
)

// ErrLegacyServer is returned only after successful protocol negotiation and
// before authentication when the server lacks modern logon capabilities.
// It must never be used for an authentication or transport failure.
var ErrLegacyServer = errors.New("server requires legacy Oracle authentication protocol")

// WithLegacyServerDetection lets an application select its legacy driver before
// this connector attempts authentication. Failed negotiation stays an error.
func (connector *OracleConnector) WithLegacyServerDetection() {
	connector.detectLegacyServer = true
}

func (conn *Connection) checkLegacyServer(authenticationService string) error {
	if !conn.detectLegacyServer || authMayHaveStarted(conn.connOption, authenticationService) {
		return nil
	}
	if conn.tcpNego != nil && len(conn.tcpNego.ServerCompileTimeCaps) >= 8 && conn.tcpNego.ServerCompileTimeCaps[4]&8 == 0 {
		return ErrLegacyServer
	}
	return nil
}

// External authentication can complete during native negotiation. A wallet or
// client TLS certificate may authenticate earlier at the transport layer.
func authMayHaveStarted(config *configurations.ConnectionConfig, service string) bool {
	return service != "" || config.AuthType != configurations.Normal || config.Wallet != nil ||
		(config.TLSConfig != nil && (len(config.TLSConfig.Certificates) > 0 || config.TLSConfig.GetClientCertificate != nil))
}
