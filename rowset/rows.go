// Package rowset consumes caller-native database/sql rows without owning a
// connection, driver, lease, retry or recovery policy.
package rowset

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/marcuoli/go-oracle-session/session"
)

// Queryer is a caller-admitted native handle, commonly *sql.Conn or *sql.Tx.
// Its QueryContext must return the real database/sql cursor it owns.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Cursor is the single-owner iteration contract shared by native *sql.Rows and
// lease-owning streams. Read takes ownership of closing it. Do not iterate or
// close it concurrently; the source retains its existing cancellation policy.
// Cursors exposing Complete() bool must establish it before JSON can succeed.
type Cursor interface {
	Columns() ([]string, error)
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

// Kind is the logical scalar contract, not a replacement for ColumnTypes.
type Kind string

const (
	// Text preserves VARCHAR-style source characters and SQL NULL.
	Text Kind = "text"
	// Integer preserves exact signed64-bit values; floats/overflow are refused.
	Integer Kind = "integer"
)

// Column preserves its position in the native projection. Text columns remain
// strings, integer columns become exact JSON integers, and either can be NULL.
type Column struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
}

// Table preserves row/column order and SQL NULL as nil. Read returns no partial
// Table when iteration, conversion, finalization, cancellation or budgets fail.
type Table struct {
	Columns []Column `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Limits bounds retained JSON data, never the caller-owned native stream.
// Zero fields select the positive defaults; negative/excessive values are invalid.
type Limits struct {
	MaxRows  int
	MaxBytes int
}

// Default and maximum limits bound JSON consumption, not the raw native stream.
const (
	DefaultMaxRows  = 10000
	DefaultMaxBytes = 8 << 20
	MaxRows         = 1000000
	MaxBytes        = 64 << 20
)

var (
	// ErrInvalidRequest rejects an absent native handle/schema or invalid limits.
	ErrInvalidRequest = errors.New("invalid native row request")
	// ErrInvalidEvidence rejects mismatched metadata or unsupported exact scalar values.
	ErrInvalidEvidence = errors.New("invalid native row evidence")
	// ErrCapacity means the complete selected result exceeds the JSON row/byte budget.
	ErrCapacity = errors.New("row result exceeds declared capacity")
)

// Error has a static operation message. Unwrap retains all causes exposed by
// database/sql for errors.Is/As and explicit private diagnostics.
type Error struct {
	Operation string
	cause     error
}

// Error formats only the static operation, omitting original cause details.
func (e *Error) Error() string { return "native row " + e.Operation + " failed" }

// Unwrap preserves the original reported causes for errors.Is/As.
func (e *Error) Unwrap() error { return e.cause }

// Resolve applies positive JSON limits; there is no unbounded materialization.
func Resolve(limits Limits) (Limits, error) {
	if limits.MaxRows < 0 || limits.MaxRows > MaxRows || limits.MaxBytes < 0 || limits.MaxBytes > MaxBytes {
		return Limits{}, ErrInvalidRequest
	}
	if limits.MaxRows == 0 {
		limits.MaxRows = DefaultMaxRows
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = DefaultMaxBytes
	}
	return limits, nil
}

// Query delegates to the provided native handle and returns its cursor unchanged.
// It creates no child context that could be canceled on return. The caller must
// iterate/close and honor its own stream budgets, transport and cleanup policy.
func Query(ctx context.Context, queryer Queryer, statement string, args ...any) (*sql.Rows, error) {
	if queryer == nil || (reflect.ValueOf(queryer).Kind() == reflect.Pointer && reflect.ValueOf(queryer).IsNil()) {
		return nil, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "query", cause: err}
	}
	rows, err := queryer.QueryContext(ctx, statement, args...)
	err = errors.Join(err, ctx.Err())
	if err != nil {
		if rows != nil {
			err = errors.Join(err, rows.Err(), rows.Close(), rows.Err())
		}
		return nil, &Error{Operation: "query", cause: err}
	}
	if rows == nil {
		return nil, &Error{Operation: "query", cause: ErrInvalidEvidence}
	}
	return rows, nil
}

// Read owns and finalizes rows on every path. Native column positions must match
// the declared fixed schema; metadata casing and source row order are retained.
// An owned cursor must also report complete EOF through its existing lifecycle.
// Limits count exact standard-JSON encoded bytes as rows arrive. No driver,
// hidden Close error recovery or concurrent cursor engine is introduced here.
func Read(ctx context.Context, rows Cursor, schema []Column, limits Limits) (table Table, returnedErr error) {
	if rows == nil || (reflect.ValueOf(rows).Kind() == reflect.Pointer && reflect.ValueOf(rows).IsNil()) {
		return Table{}, ErrInvalidRequest
	}
	operation := "read"
	defer func() {
		cause := errors.Join(returnedErr, rows.Err(), rows.Close(), rows.Err(), ctx.Err())
		// Owned streams distinguish full EOF from an early close. Native rows
		// have no such flag, so their conventional single-owner lifecycle applies.
		if completed, ok := rows.(interface{ Complete() bool }); ok && !completed.Complete() {
			cause = errors.Join(cause, ErrInvalidEvidence)
		}
		if cause != nil {
			table = Table{}
			returnedErr = &Error{Operation: operation, cause: cause}
		}
	}()
	resolved, err := Resolve(limits)
	if err != nil {
		return Table{}, err
	}
	if err = ctx.Err(); err != nil {
		return Table{}, err
	}
	if len(schema) == 0 || len(schema) > 64 {
		return Table{}, ErrInvalidRequest
	}
	columns, err := rows.Columns()
	if err != nil {
		operation = "columns"
		return Table{}, err
	}
	if len(columns) != len(schema) {
		return Table{}, ErrInvalidEvidence
	}
	table.Columns = make([]Column, len(columns))
	table.Rows = make([][]any, 0)
	for i, column := range schema {
		if !utf8.ValidString(columns[i]) || !strings.EqualFold(columns[i], column.Name) || (column.Kind != Text && column.Kind != Integer) {
			return Table{}, ErrInvalidEvidence
		}
		table.Columns[i] = Column{Name: strings.Clone(columns[i]), Kind: column.Kind}
	}
	header, err := json.Marshal(table)
	if err != nil {
		return Table{}, err
	}
	bytes := len(header)
	if bytes > resolved.MaxBytes {
		return Table{}, ErrCapacity
	}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return Table{}, err
		}
		if len(table.Rows) >= resolved.MaxRows {
			operation = "budget"
			return Table{}, ErrCapacity
		}
		values := make([]any, len(schema))
		dest := make([]any, len(schema))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			operation = "scan"
			return Table{}, err
		}
		for i, column := range schema {
			values[i], err = scalar(values[i], column.Kind)
			if err != nil {
				operation = "value"
				return Table{}, err
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			operation = "encode"
			return Table{}, err
		}
		bytes += len(encoded)
		if len(table.Rows) > 0 {
			bytes++
		}
		if bytes > resolved.MaxBytes {
			operation = "budget"
			return Table{}, ErrCapacity
		}
		table.Rows = append(table.Rows, values)
	}
	return table, nil
}

// ReadJSON independently consumes/finalizes its supplied cursor, then uses the
// standard encoder. Errors always return nil JSON, including post-read cancellation.
func ReadJSON(ctx context.Context, rows Cursor, schema []Column, limits Limits) (json.RawMessage, error) {
	table, err := Read(ctx, rows, schema, limits)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, &Error{Operation: "encode", cause: err}
	}
	data, err := json.Marshal(table)
	if err != nil {
		return nil, &Error{Operation: "encode", cause: err}
	}
	if err = ctx.Err(); err != nil {
		return nil, &Error{Operation: "encode", cause: err}
	}
	return json.RawMessage(data), nil
}

func scalar(value any, kind Kind) (any, error) {
	if value == nil {
		return nil, nil
	}
	if kind == Text {
		var text string
		switch value := value.(type) {
		case string:
			text = strings.Clone(value)
		case []byte:
			text = string(value)
		default:
			return nil, ErrInvalidEvidence
		}
		if !utf8.ValidString(text) {
			return nil, ErrInvalidEvidence
		}
		return text, nil
	}
	exact, err := session.ExactValue(value)
	if err != nil {
		return nil, err
	}
	if exact.Null {
		return nil, nil
	}
	integer, err := strconv.ParseInt(exact.Text, 10, 64)
	if err != nil {
		return nil, ErrInvalidEvidence
	}
	return integer, nil
}

// QueryJSON executes caller-supplied SQL on a borrowed native handle, then owns
// complete cursor consumption through ReadJSON. Invalid limits fail before SQL;
// other failures return nil JSON. The caller retains SQL admission, connection,
// query-context and physical cleanup ownership; no child watcher is introduced.
func QueryJSON(ctx context.Context, queryer Queryer, statement string, schema []Column, limits Limits, args ...any) (json.RawMessage, error) {
	resolved, err := Resolve(limits)
	if err != nil {
		return nil, err
	}
	rows, err := Query(ctx, queryer, statement, args...)
	if err != nil {
		return nil, err
	}
	return ReadJSON(ctx, rows, schema, resolved)
}
