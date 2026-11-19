package compat

import (
	"context"
	"database/sql/driver"

	modern "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3"
	modernNetwork "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/network"
)

type modernDriverConnector struct {
	*modern.OracleConnector
	setupErr error
	fresh    func() *modernDriverConnector
}

func (c *modernDriverConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.setupErr != nil {
		return nil, c.setupErr
	}
	if c.fresh != nil {
		return c.fresh().Connect(ctx)
	}
	conn, err := c.OracleConnector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	native := conn.(*modern.Connection)
	return &modernDriverConnection{Connection: native, health: &connectionHealth{discard: native.Discard}}, nil
}

type modernDriverConnection struct {
	*modern.Connection
	health *connectionHealth
}

func (*modernDriverConnection) CompatibilityMode() Mode { return V3 }

// BreakCurrentOperation requests an ordinary break on the owned connection.
// The owner must join operation/break completion before checking settlement.
func (c *modernDriverConnection) BreakCurrentOperation() error {
	breaker, ok := c.GetSession().(interface{ BreakConnection() error })
	if !ok {
		return ErrUnsupported
	}
	return breaker.BreakConnection()
}

// CancellationSettled reports driver state after joined operation/break work.
// It alone is insufficient for reuse without the typed server acknowledgement.
func (c *modernDriverConnection) CancellationSettled() bool {
	state, ok := c.GetSession().(interface{ IsBreak() bool })
	return ok && c.IsValid() && !state.IsBreak()
}
func (c *modernDriverConnection) CheckNamedValue(v *driver.NamedValue) error {
	if err := adaptNamedValue(v, V3, c.health); err != nil {
		return err
	}
	return c.Connection.CheckNamedValue(v)
}
func (c *modernDriverConnection) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Connection.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &modernDriverStatement{Stmt: stmt.(*modern.Stmt), health: c.health}, nil
}
func (c *modernDriverConnection) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	// Native PrepareContext can reopen a disconnected connection. Reject it here
	// so a retired physical lease never starts another authentication attempt.
	session, ok := c.GetSession().(*modernNetwork.Session)
	if !c.IsValid() || !ok || session == nil || !session.Connected {
		return nil, driver.ErrBadConn
	}
	stmt, err := c.Connection.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &modernDriverStatement{Stmt: stmt.(*modern.Stmt), health: c.health}, nil
}

type modernDriverStatement struct {
	*modern.Stmt
	health *connectionHealth
}

func (s *modernDriverStatement) CheckNamedValue(v *driver.NamedValue) error {
	if err := adaptNamedValue(v, V3, s.health); err != nil {
		return err
	}
	return s.Stmt.CheckNamedValue(v)
}

func (c *modernDriverConnection) IsValid() bool { return c.health.valid() && c.Connection.IsValid() }
func (c *modernDriverConnection) Close() error  { return c.health.close(c.Connection.Close) }
func (s *modernDriverStatement) Close() error {
	if !s.health.valid() {
		return nil
	}
	return s.health.fail(s.Stmt.Close())
}

func newConfiguredModernConnector(dsn string, options Options) *modernDriverConnector {
	native := func() *modern.OracleConnector {
		connector := modern.NewConnector(dsn).(*modern.OracleConnector)
		connector.WithLoginWarningHandler(options.LoginWarningHandler)
		if options.Mode == Negotiated {
			connector.WithLegacyServerDetection()
		}
		if options.Dialer != nil {
			connector.Dialer(options.Dialer)
		}
		return connector
	}
	connector := newModernDriverConnector(native())
	// Native drivers learn per-connection metadata during admission. A fresh
	// registry per physical Connect avoids sharing that mutable initialization.
	connector.fresh = func() *modernDriverConnector { return newModernDriverConnector(native()) }
	return connector
}
