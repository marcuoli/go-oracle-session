package compat

import (
	"context"
	"database/sql/driver"
)

// A validator must never retain a connection whose transaction completion
// failed: a canceled lifetime can prevent the native rollback from being sent.
type ownedTransaction struct {
	driver.Tx
	health *connectionHealth
}

func (tx *ownedTransaction) Commit() error   { return tx.health.fail(tx.Tx.Commit()) }
func (tx *ownedTransaction) Rollback() error { return tx.health.fail(tx.Tx.Rollback()) }

func (c *legacyDriverConnection) Begin() (driver.Tx, error) {
	tx, err := c.Connection.Begin()
	if err != nil {
		return nil, err
	}
	return &ownedTransaction{Tx: tx, health: c.health}, nil
}
func (c *legacyDriverConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Connection.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &ownedTransaction{Tx: tx, health: c.health}, nil
}
func (c *modernDriverConnection) Begin() (driver.Tx, error) {
	tx, err := c.Connection.Begin()
	if err != nil {
		return nil, err
	}
	return &ownedTransaction{Tx: tx, health: c.health}, nil
}
func (c *modernDriverConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Connection.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &ownedTransaction{Tx: tx, health: c.health}, nil
}
