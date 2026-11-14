package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"
)

// ErrUnsupported identifies an unavailable contextual driver capability.
var ErrUnsupported = errors.New("driver context capability is unavailable")

// Capabilities reports observed connection interfaces, not database qualification.
// Prepared statements negotiate their query, execution and bind support separately.
type Capabilities struct {
	DirectQuery, DirectExec, PreparedContext, Transactions, NamedValues bool
}

type driverState struct {
	mu           sync.Mutex
	capabilities Capabilities
	transaction  *trackedTx
}

// Capabilities returns the interfaces observed during connection admission.
func (s *Session) Capabilities() Capabilities { return s.driverState.capabilities }

type trackedConnector struct {
	driver.Connector
	errors  *cursorErrors
	options Options
	state   *driverState
}

func (c trackedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	_, q := conn.(driver.QueryerContext)
	_, e := conn.(driver.ExecerContext)
	_, p := conn.(driver.ConnPrepareContext)
	_, tx := conn.(driver.ConnBeginTx)
	_, n := conn.(driver.NamedValueChecker)
	if (!c.options.PreparedContext && !q) || (c.options.PreparedContext && !p) {
		return nil, errors.Join(ErrUnsupported, conn.Close())
	}
	c.state.capabilities = Capabilities{DirectQuery: q, DirectExec: e, PreparedContext: p, Transactions: tx, NamedValues: n}
	base := &trackedConnection{Conn: conn, errors: c.errors, options: c.options, state: c.state}
	// The prepared mode deliberately advertises no direct query/exec interface.
	// database/sql then invokes its one-shot contextual prepare branch and the
	// statement's bind checker, without a retrying exported sql.Stmt helper.
	if c.options.PreparedContext {
		return &preparedConnection{base}, nil
	}
	return &directConnection{base}, nil
}

type trackedConnection struct {
	driver.Conn
	errors  *cursorErrors
	options Options
	state   *driverState
}
type directConnection struct{ *trackedConnection }

func (c *directConnection) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, text, args)
	if errors.Is(err, driver.ErrSkip) {
		return nil, ErrUnsupported
	}
	return c.trackRows(rows, err)
}
func (c *directConnection) ExecContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Result, error) {
	exec, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, ErrUnsupported
	}
	result, err := exec.ExecContext(ctx, text, args)
	if result == nil && err == nil {
		return nil, errors.New("driver returned nil execution result")
	}
	if errors.Is(err, driver.ErrSkip) {
		return nil, ErrUnsupported
	}
	return result, err
}
func (c *trackedConnection) trackRows(rows driver.Rows, err error) (driver.Rows, error) {
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("driver returned nil cursor")
	}
	return &trackedRows{Rows: rows, errors: c.errors}, nil
}
func (c *trackedConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	begin, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, ErrUnsupported
	}
	tx, err := begin.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	if tx == nil {
		return nil, errors.New("driver returned nil transaction")
	}
	tracked := &trackedTx{Tx: tx, done: make(chan struct{})}
	c.state.mu.Lock()
	c.state.transaction = tracked
	c.state.mu.Unlock()
	return tracked, nil
}

type trackedTx struct {
	driver.Tx
	done   chan struct{}
	action string
	err    error
}

func (tx *trackedTx) Commit() error {
	tx.action = "commit"
	tx.err = tx.Tx.Commit()
	close(tx.done)
	return tx.err
}
func (tx *trackedTx) Rollback() error {
	tx.action = "rollback"
	tx.err = tx.Tx.Rollback()
	close(tx.done)
	return tx.err
}

type preparedConnection struct{ *trackedConnection }

func (c *preparedConnection) PrepareContext(ctx context.Context, text string) (driver.Stmt, error) {
	stmt, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, text)
	if err != nil {
		return nil, err
	}
	if stmt == nil {
		return nil, errors.New("driver returned nil statement")
	}
	return &trackedStatement{Stmt: stmt, connection: c.trackedConnection}, nil
}

// Prepare refuses legacy fallback even when a driver embeds legacy interfaces.
func (c *preparedConnection) Prepare(string) (driver.Stmt, error) { return nil, ErrUnsupported }

type trackedStatement struct {
	driver.Stmt
	connection *trackedConnection
}

func (s *trackedStatement) Exec([]driver.Value) (driver.Result, error) { return nil, ErrUnsupported }
func (s *trackedStatement) Query([]driver.Value) (driver.Rows, error)  { return nil, ErrUnsupported }
func (s *trackedStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	exec, ok := s.Stmt.(driver.StmtExecContext)
	if !ok {
		return nil, ErrUnsupported
	}
	r, err := exec.ExecContext(ctx, args)
	if r == nil && err == nil {
		return nil, errors.New("driver returned nil execution result")
	}
	if errors.Is(err, driver.ErrSkip) {
		err = ErrUnsupported
	}
	return r, err
}
func (s *trackedStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	query, ok := s.Stmt.(driver.StmtQueryContext)
	if !ok {
		return nil, ErrUnsupported
	}
	rows, err := query.QueryContext(ctx, args)
	if errors.Is(err, driver.ErrSkip) {
		err = ErrUnsupported
	}
	return s.connection.trackRows(rows, err)
}
func (s *trackedStatement) CheckNamedValue(value *driver.NamedValue) error {
	if check, ok := s.Stmt.(driver.NamedValueChecker); ok {
		return check.CheckNamedValue(value)
	}
	if check, ok := s.connection.Conn.(driver.NamedValueChecker); ok {
		return check.CheckNamedValue(value)
	}
	return driver.ErrSkip
}
func (s *trackedStatement) ColumnConverter(index int) driver.ValueConverter {
	if c, ok := s.Stmt.(driver.ColumnConverter); ok {
		return c.ColumnConverter(index)
	}
	return driver.DefaultParameterConverter
}
func (s *trackedStatement) Close() error {
	err := boundedCleanup(context.Background(), s.connection.options, s.Stmt.Close)
	s.connection.errors.add(err)
	return err
}

type commandRunner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Session) runner() commandRunner {
	if s.commands != nil {
		return s.commands
	}
	return s.connection
}
