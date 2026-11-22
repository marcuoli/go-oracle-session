package compat

import (
	"context"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"errors"

	legacyNegotiation "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/advanced_nego"
	legacyNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
)

// DriverName registers the default V2 compatibility adapter with database/sql.
// NewConnector supplies explicit per-connector options without global policy.
const DriverName = "go-oracle-session"

func init() { sql.Register(DriverName, &compatibilityDriver{}) }

// Connector owns immutable backend configuration. It supports concurrent
// Connect calls; each call retains its original context and admission identity.
type Connector struct {
	mode           Mode
	legacy, modern driver.Connector
	owner          *compatibilityDriver
}

// NewConnector prepares the selected compatibility policy without network I/O.
// It removes FAST LOGIN for V2 and enforces full login for V3, preserving all
// other URL values and identity. Configure caller options before concurrent use.
func NewConnector(dsn string, options Options) (*Connector, error) {
	return newConnector(dsn, options, nil)
}

// NewConnectorWithTLSConfig applies config to the physical TLS connections
// selected by the DSN. A nil config uses the same TLS policy as NewConnector.
// The connector takes a shallow snapshot and clones it for each physical
// connection. Keep referenced trust material and callbacks safe for concurrent
// use. The supplied config takes precedence over DSN SSL VERIFY and the
// driver's wallet-derived TLS settings. The Oracle Net driver uses the
// addressed host as the TLS server name.
func NewConnectorWithTLSConfig(dsn string, options Options, config *tls.Config) (*Connector, error) {
	return newConnector(dsn, options, config.Clone())
}

func newConnector(dsn string, options Options, tlsConfig *tls.Config) (*Connector, error) {
	if options.Mode > Automatic {
		return nil, ErrInvalidOptions
	}
	c := &Connector{mode: options.Mode, owner: &compatibilityDriver{options: options, tlsConfig: tlsConfig}}
	if options.Mode != V3 {
		prepared, err := prepareDSN(dsn, V2)
		if err != nil {
			return nil, err
		}
		c.legacy = newLegacyDriverConnector(prepared, options, tlsConfig)
	}
	if options.Mode != V2 {
		prepared, err := prepareDSN(dsn, V3)
		if err != nil {
			return nil, err
		}
		c.modern = newConfiguredModernConnector(prepared, options, tlsConfig)
	}
	return c, nil
}

// Mode reports configured policy. ConnectionMode reports the backend used
// by a physical connection, which can differ in a selecting policy.
func (c *Connector) Mode() Mode { return c.mode }

// Driver returns the compatibility database/sql factory, never a native backend.
func (c *Connector) Driver() driver.Driver { return c.owner }

// Connect authenticates at most one backend per call. Automatic and Negotiated
// can select the alternate once after a specific pre-authentication signal and
// successful transport cleanup. Authentication and SQL failures are terminal.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.connect(ctx, ConnectionObserver{}, nil)
}

func (c *Connector) connect(ctx context.Context, observer ConnectionObserver, attempts *[]ConnectionAttempt) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	primary, alternate := c.modern, c.legacy
	firstMode, nextMode := V3, V2
	var signal error
	switch c.mode {
	case V2, Automatic:
		primary, alternate = c.legacy, c.modern
		firstMode, nextMode = V2, V3
		if c.mode == Automatic {
			signal = legacyNegotiation.ErrClientCryptoVersion
		}
	case Negotiated:
		signal = modern.ErrLegacyServer
	}
	conn, err := connectAttempt(ctx, primary, firstMode, observer, attempts)
	if signal == nil || conn != nil || !onlySelectionSignal(err, signal) {
		conn, err = finishConnection(conn, err)
		if signal != nil {
			err = preventConnectRetry(err)
		}
		return conn, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	conn, alternateErr := finishConnection(connectAttempt(ctx, alternate, nextMode, observer, attempts))
	if c.mode == Automatic && alternateErr != nil {
		alternateErr = errors.Join(err, alternateErr)
	}
	return conn, preventConnectRetry(alternateErr)
}

func finishConnection(conn driver.Conn, err error) (driver.Conn, error) {
	if err != nil {
		if conn != nil {
			if closeErr := conn.Close(); closeErr != nil {
				return nil, errors.Join(err, closeErr)
			}
		}
		return nil, err
	}
	if conn == nil {
		return nil, ErrNoConnection
	}
	return conn, nil
}

func onlySelectionSignal(err, signal error) bool {
	for err != nil {
		if err == signal {
			return true
		}
		// Even a joined signal with a cleanup failure is not safe selection evidence.
		if _, ok := err.(interface{ Unwrap() []error }); ok {
			return false
		}
		err = errors.Unwrap(err)
	}
	return false
}

type compatibilityDriver struct {
	options   Options
	tlsConfig *tls.Config
}

func (d *compatibilityDriver) Open(dsn string) (driver.Conn, error) {
	c, err := newConnector(dsn, d.options, d.tlsConfig)
	if err != nil {
		return nil, err
	}
	return c.Connect(context.Background())
}
func (d *compatibilityDriver) OpenConnector(dsn string) (driver.Connector, error) {
	return newConnector(dsn, d.options, d.tlsConfig)
}

// IsDriver identifies the compatibility pool factory without opening a connection.
func IsDriver(provider driver.Driver) bool { _, ok := provider.(*compatibilityDriver); return ok }

// OracleCode finds a typed Oracle error from either retained backend through
// wrapping/joined errors. It does not authorize driver selection or replay.
func OracleCode(err error) (int, bool) {
	var old *legacyNetwork.OracleError
	if errors.As(err, &old) && old != nil {
		return old.ErrCode, true
	}
	var current *modernNetwork.OracleError
	if errors.As(err, &current) && current != nil {
		return current.ErrCode, true
	}
	return 0, false
}

var _ driver.Connector = (*Connector)(nil)
var _ driver.DriverContext = (*compatibilityDriver)(nil)
