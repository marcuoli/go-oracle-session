//go:build integration && goora2

package oracle

import (
	"database/sql/driver"
	"errors"
	"github.com/marcuoli/go-oracle-session/transport"
	goora "github.com/sijms/go-ora/v2"
	nativeNetwork "github.com/sijms/go-ora/v2/network"
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

func nativeCancellationAcknowledged(err error) bool {
	var native *nativeNetwork.OracleError
	return errors.As(err, &native) && native != nil && native.ErrCode == 1013
}

func nativeSoftControl(conn driver.Conn) (nativeSoftConnection, bool) {
	soft, ok := conn.(nativeSoftConnection)
	return soft, ok
}
