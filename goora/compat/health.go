package compat

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Connection health belongs to one physical lease, never to a connector.
// Terminal LOB failures are reported separately from Exec's result so they
// cannot turn successful execution into database/sql's retry signal.
type connectionHealth struct {
	invalid  atomic.Bool
	once     sync.Once
	discard  func() error
	closeErr error
}

func (h *connectionHealth) valid() bool { return h == nil || !h.invalid.Load() }
func (h *connectionHealth) fail(cause error) error {
	if cause == nil || h == nil {
		return cause
	}
	h.invalid.Store(true)
	h.once.Do(func() {
		if h.discard != nil {
			h.closeErr = h.discard()
		}
	})
	if h.closeErr != nil {
		return errors.Join(cause, h.closeErr)
	}
	return cause
}
func (h *connectionHealth) close(normal func() error) error {
	if h == nil {
		return normal()
	}
	h.once.Do(func() {
		terminal := h.invalid.Swap(true)
		if terminal && h.discard != nil {
			h.closeErr = h.discard()
		} else {
			h.closeErr = normal()
		}
	})
	return h.closeErr
}
