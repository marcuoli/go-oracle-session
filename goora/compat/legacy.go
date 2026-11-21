package compat

import (
	"context"
	"crypto/tls"
	"database/sql/driver"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
)

type legacyDriverConnector struct {
	*legacy.OracleConnector
	fresh func() *legacy.OracleConnector
}

func (c *legacyDriverConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connector := c.OracleConnector
	if c.fresh != nil {
		connector = c.fresh()
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	native := conn.(*legacy.Connection)
	return &legacyDriverConnection{Connection: native, health: &connectionHealth{discard: native.Discard}}, nil
}

type legacyDriverConnection struct {
	*legacy.Connection
	health *connectionHealth
}

func (*legacyDriverConnection) CompatibilityMode() Mode { return V2 }
func (c *legacyDriverConnection) CheckNamedValue(v *driver.NamedValue) error {
	if err := adaptNamedValue(v, V2, c.health); err != nil {
		return err
	}
	return c.Connection.CheckNamedValue(v)
}
func (c *legacyDriverConnection) Prepare(query string) (driver.Stmt, error) {
	stmt, err := c.Connection.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &legacyDriverStatement{Stmt: stmt.(*legacy.Stmt), connection: c.Connection, query: query, health: c.health}, nil
}
func (c *legacyDriverConnection) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	stmt, err := c.Connection.PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	return &legacyDriverStatement{Stmt: stmt.(*legacy.Stmt), connection: c.Connection, query: query, health: c.health}, nil
}

// Legacy servers reject optimized repeated prepared execution with ORA-03106.
// Fresh cursors apply only before a later independent execution; failures are
// never replayed and authentication/connection identity remain unchanged.
type legacyDriverStatement struct {
	*legacy.Stmt
	connection *legacy.Connection
	query      string
	health     *connectionHealth
	used       bool
}

func (s *legacyDriverStatement) CheckNamedValue(v *driver.NamedValue) error {
	if err := adaptNamedValue(v, V2, s.health); err != nil {
		return err
	}
	return s.Stmt.CheckNamedValue(v)
}
func (s *legacyDriverStatement) prepareExecution() error {
	if !s.used {
		s.used = true
		return nil
	}
	if err := s.Stmt.Close(); err != nil {
		return s.health.fail(err)
	}
	s.Stmt = legacy.NewStmt(s.query, s.connection)
	return nil
}
func (s *legacyDriverStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if err := s.prepareExecution(); err != nil {
		return nil, err
	}
	return s.Stmt.ExecContext(ctx, args)
}
func (s *legacyDriverStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if err := s.prepareExecution(); err != nil {
		return nil, err
	}
	return s.Stmt.QueryContext(ctx, args)
}
func namedValues(args []driver.Value) []driver.NamedValue {
	values := make([]driver.NamedValue, len(args))
	for i, v := range args {
		values[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return values
}
func (s *legacyDriverStatement) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedValues(args))
}
func (s *legacyDriverStatement) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedValues(args))
}

func (c *legacyDriverConnection) IsValid() bool {
	return c.health.valid() && c.Connection.CancellationSettled()
}
func (c *legacyDriverConnection) Close() error {
	// Native terminal failures can invalidate state before a wrapper error hook runs.
	// Never send ordinary logoff on that uncertain protocol.
	if c.health != nil && !c.IsValid() {
		c.health.invalid.Store(true)
	}
	return c.health.close(c.Connection.Close)
}
func (s *legacyDriverStatement) Close() error {
	if !s.health.valid() {
		return nil
	}
	return s.health.fail(s.Stmt.Close())
}

func newLegacyDriverConnector(dsn string, options Options, tlsConfig *tls.Config) *legacyDriverConnector {
	factory := func() *legacy.OracleConnector {
		connector := legacy.NewConnector(dsn).(*legacy.OracleConnector)
		connector.WithLoginWarningHandler(options.LoginWarningHandler)
		connector.WithTLSConfig(tlsConfig.Clone())
		if options.Dialer != nil {
			connector.Dialer(options.Dialer)
		}
		return connector
	}
	return &legacyDriverConnector{OracleConnector: factory(), fresh: factory}
}
