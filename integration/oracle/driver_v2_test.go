//go:build integration && goora2

package oracle

import (
	"database/sql/driver"
	"github.com/marcuoli/go-oracle-session/transport"
	goora "github.com/sijms/go-ora/v2"
)

// The upstream majors both register the same database/sql name during init.
// Build separate explicit targets instead of patching drivers or retrying login.
const nativeDriver = "go-ora/v2"

func nativeConnector(dsn string, sockets *transport.Dialer) driver.Connector {
	connector := goora.NewConnector(dsn)
	connector.(*goora.OracleConnector).Dialer(sockets)
	return connector
}

func nativeCharacterOutput(destination *string, capacity int) any {
	return goora.Out{Dest: destination, Size: capacity}
}
