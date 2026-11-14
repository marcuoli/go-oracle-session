//go:build integration && !goora2

package oracle

import (
	"database/sql/driver"
	"github.com/marcuoli/go-oracle-session/transport"
	goora "github.com/sijms/go-ora/v3"
)

// Keep the upstream majors in separate binaries: both register "oracle" at init.
const nativeDriver = "go-ora/v3"

func nativeConnector(dsn string, sockets *transport.Dialer) driver.Connector {
	connector := goora.NewConnector(dsn)
	connector.(*goora.OracleConnector).Dialer(sockets)
	return connector
}

func nativeCharacterOutput(destination *string, capacity int) any {
	return goora.Out{Dest: destination, Size: capacity}
}
