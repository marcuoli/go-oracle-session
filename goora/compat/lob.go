package compat

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"

	legacy "github.com/marcuoli/go-oracle-session/internal/compatdriver/v2"
	modernConfig "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/configurations"
	modernTypes "github.com/marcuoli/go-oracle-session/internal/compatdriver/v3/types"
)

// Clob owns a selected backend's CLOB value. Do not copy it after use or use it
// concurrently. Keep the physical connection and statement/cursor leased until
// Read, Value and Free finish; consume row values before advancing the cursor.
// V2 materializes output and its statement owns locator disposal. V3 can retain
// a lazy locator that Read/Free accesses on that same physical connection.
// Read/Free failures discard their physical owner without replay or a logoff
// exchange on an uncertain protocol. This does not prove remote SQL termination.
// Neither mode promises bounded whole-value memory usage.
type Clob struct {
	legacy               *legacy.Clob
	modern               *modernTypes.Clob
	mode                 Mode
	bound, closed, ready bool
	readErr              error
	owner                *connectionHealth
	discarded            bool
}

// SetValue starts an input value from nil, text or bytes. Free a previously
// bound value first. The input is materialized; no network call is made.
func (c *Clob) SetValue(value any) error {
	if c == nil {
		return fmt.Errorf("nil Oracle CLOB")
	}
	if c.bound && !c.closed {
		return ErrLOBInUse
	}
	if bytes, ok := value.([]byte); ok {
		value = string(bytes)
	}
	old := &legacy.Clob{}
	if err := old.Scan(value); err != nil {
		return err
	}
	current := &modernTypes.Clob{}
	if err := current.SetValue(value); err != nil {
		return err
	}
	// The native string encoder can return nil for an empty string. Retain its
	// distinct, non-NULL in-memory value; actual bind semantics remain native.
	if text, ok := value.(string); ok && text == "" {
		current.SetBytes([]byte{})
	}
	c.legacy, c.modern, c.mode, c.bound, c.closed, c.ready, c.readErr = old, current, V2, false, false, true, nil
	c.owner = nil
	c.discarded = false
	return nil
}
func (c *Clob) native(mode Mode, owners ...*connectionHealth) (any, error) {
	if c == nil {
		return nil, fmt.Errorf("nil Oracle CLOB destination")
	}
	if c.bound && !c.closed {
		return nil, ErrLOBInUse
	}
	if c.closed {
		c.legacy, c.modern, c.ready, c.readErr = nil, nil, false, nil
	}
	c.mode, c.bound, c.closed = mode, true, false
	c.owner = nil
	if len(owners) > 0 {
		c.owner = owners[0]
	}
	c.discarded = false
	if mode == V2 {
		if c.legacy == nil {
			c.legacy = &legacy.Clob{}
		}
		return c.legacy, nil
	}
	if c.modern == nil {
		c.modern = &modernTypes.Clob{}
	}
	return c.modern, nil
}

// Read materializes a lazy modern value with the supplied operation context.
// Legacy/prefetched values need no extra read. A failed read is sticky: it
// exposes no partial Value and never silently retries native I/O.
func (c *Clob) Read(ctx context.Context) error {
	if c.closed {
		return ErrLOBClosed
	}
	if c.readErr != nil {
		return c.readErr
	}
	if !c.owner.valid() {
		return ErrLOBClosed
	}
	if c.mode == V3 && c.modern != nil && !c.modern.IsQuasi() && c.modern.GetReadMode() != modernConfig.LobReadMode_AUTO {
		c.readErr = c.fail(c.modern.Read(ctx))
	}
	c.ready = c.readErr == nil
	return c.readErr
}

// Value returns exact text or SQL NULL after required Read work has completed.
// Unread/failed lazy values expose no prefix. It does not release resources;
// discard output values whenever their execution failed.
func (c *Clob) Value() (driver.Value, error) {
	if c.closed {
		return nil, ErrLOBClosed
	}
	if c.readErr != nil {
		return nil, c.readErr
	}
	if !c.owner.valid() {
		return nil, ErrLOBClosed
	}
	if c.mode == V3 && c.modern != nil {
		lazy := !c.modern.IsQuasi()
		if lazy && c.modern.GetReadMode() != modernConfig.LobReadMode_AUTO && !c.ready {
			return nil, ErrLOBUnread
		}
		value, err := c.modern.Value()
		// A real locator identifies an allocated empty CLOB even when its successful
		// zero-byte read produced nil bytes; an absent locator remains SQL NULL.
		if err == nil && value == nil && lazy && len(c.modern.GetLocator()) > 0 {
			return "", nil
		}
		return value, err
	}
	if c.legacy == nil || !c.legacy.Valid {
		return nil, nil
	}
	return c.legacy.String, nil
}

// Free releases an owned modern locator once. Legacy locators remain owned by
// the statement/cursor, which the caller must still close. Free invalidates this
// value even on error; its failure participates in caller disposal/retirement.
func (c *Clob) Free() error {
	if c.closed {
		return nil
	}
	c.closed = true
	if c.discarded || !c.owner.valid() {
		return nil
	}
	if c.mode == V3 && c.modern != nil {
		return c.fail(c.modern.Free())
	}
	return nil
}

// Scan accepts a native CLOB or materialized text/NULL. Consume/free the previous
// value before scanning another; native row values retain their cursor lifetime.
func (c *Clob) Scan(value any) error {
	if c.bound && !c.closed {
		return ErrLOBInUse
	}
	switch value := value.(type) {
	case legacy.Clob:
		c.legacy = &value
		c.modern = nil
		c.mode = V2
	case *legacy.Clob:
		c.legacy = value
		c.modern = nil
		c.mode = V2
	case modernTypes.Clob:
		c.modern = &value
		c.legacy = nil
		c.mode = V3
	case *modernTypes.Clob:
		c.modern = value
		c.legacy = nil
		c.mode = V3
	default:
		return c.SetValue(value)
	}
	c.bound, c.closed, c.ready, c.readErr = true, false, false, nil
	c.owner = nil
	c.discarded = false
	return nil
}
func (c *Clob) fail(cause error) error {
	if cause == nil {
		return nil
	}
	if c.owner != nil {
		c.discarded = true
		return c.owner.fail(cause)
	}
	if c.modern != nil {
		if owner, ok := c.modern.GetStreamer().(interface{ DiscardConnection() error }); ok {
			c.discarded = true
			if err := owner.DiscardConnection(); err != nil {
				return errors.Join(cause, err)
			}
		}
	}
	return cause
}
