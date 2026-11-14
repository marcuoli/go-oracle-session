//go:build integration && !goora2

package oracle

import (
	"database/sql/driver"
	"errors"
	"github.com/marcuoli/go-oracle-session/transport"
	goora "github.com/sijms/go-ora/v3"
	nativeNetwork "github.com/sijms/go-ora/v3/network"
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

func nativeCancellationAcknowledged(err error) bool {
	var native *nativeNetwork.OracleError
	return errors.As(err, &native) && native != nil && native.ErrCode == 1013
}

// Use the public session methods with their actual driver return types. No
// connection wrapper hides contextual, bind or native socket interfaces.
type nativeModernControl struct{ connection *goora.Connection }

func (c nativeModernControl) BreakCurrentOperation() error {
	return c.connection.GetSession().(interface{ BreakConnection() error }).BreakConnection()
}
func (c nativeModernControl) CancellationSettled() bool {
	return c.connection.IsValid() && !c.connection.GetSession().(interface{ IsBreak() bool }).IsBreak()
}
func nativeSoftControl(conn driver.Conn) (nativeSoftConnection, bool) {
	connection, ok := conn.(*goora.Connection)
	if !ok {
		return nil, false
	}
	_, breaks := connection.GetSession().(interface{ BreakConnection() error })
	_, settles := connection.GetSession().(interface{ IsBreak() bool })
	return nativeModernControl{connection}, breaks && settles
}
