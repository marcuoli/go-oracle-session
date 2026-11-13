package session

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync"
)

// database/sql discards a driver Rows.Close error when Next already failed.
// This forwarding boundary retains that independent cleanup cause without
// changing driver code, replaying SQL, or replacing cursor transport.
type cursorErrors struct {
	mu        sync.Mutex
	errors    []error
	closeHook func(func() error) error
}

func (c *cursorErrors) setCloseHook(hook func(func() error) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeHook = hook
}

func (c *cursorErrors) closeRows(closeRows func() error) error {
	c.mu.Lock()
	hook := c.closeHook
	c.mu.Unlock()
	var err error
	if hook == nil {
		err = closeRows()
	} else {
		err = hook(closeRows)
	}
	c.add(err)
	return err
}

func (c *cursorErrors) add(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, err)
}
func (c *cursorErrors) take() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	err := errors.Join(c.errors...)
	c.errors = nil
	return err
}
func joinCauses(first, second error) error {
	if second == nil {
		return first
	}
	if first == nil {
		return second
	}
	if errors.Is(first, second) {
		return first
	}
	return errors.Join(first, second)
}

type trackedConnector struct {
	driver.Connector
	errors *cursorErrors
}

func (c trackedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	query, ok := conn.(driver.QueryerContext)
	if !ok {
		return nil, errors.Join(errors.New("connector requires context query support"), conn.Close())
	}
	return &trackedConnection{Conn: conn, query: query, errors: c.errors}, nil
}

type trackedConnection struct {
	driver.Conn
	query  driver.QueryerContext
	errors *cursorErrors
}

func (c *trackedConnection) QueryContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.query.QueryContext(ctx, text, args)
	if err != nil {
		if errors.Is(err, driver.ErrSkip) {
			return nil, errors.New("driver declined context query; prepare fallback is not supported")
		}
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("driver returned nil cursor")
	}
	return &trackedRows{Rows: rows, errors: c.errors}, nil
}
func (c *trackedConnection) Ping(ctx context.Context) error {
	if ping, ok := c.Conn.(driver.Pinger); ok {
		return ping.Ping(ctx)
	}
	return nil
}
func (c *trackedConnection) CheckNamedValue(value *driver.NamedValue) error {
	if check, ok := c.Conn.(driver.NamedValueChecker); ok {
		return check.CheckNamedValue(value)
	}
	return driver.ErrSkip
}
func (c *trackedConnection) ResetSession(ctx context.Context) error {
	if reset, ok := c.Conn.(driver.SessionResetter); ok {
		return reset.ResetSession(ctx)
	}
	return nil
}
func (c *trackedConnection) IsValid() bool {
	if validate, ok := c.Conn.(driver.Validator); ok {
		return validate.IsValid()
	}
	return true
}

type trackedRows struct {
	driver.Rows
	errors *cursorErrors
}

func (r *trackedRows) Close() error { return r.errors.closeRows(r.Rows.Close) }
