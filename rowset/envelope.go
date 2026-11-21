package rowset

import (
	"context"
	"encoding/json"
	"errors"
)

// CursorOpener lazily opens one single-owner cursor using the supplied context.
// The operation takes ownership of any returned cursor, including on error.
// Openers retain admission, SQL, connection and cancellation policy; they must
// honor the context and must not retry an executed operation.
type CursorOpener func(context.Context) (Cursor, error)

// OpenQuery captures a borrowed handle, statement and a shallow copy of args
// without querying or validating them. The returned opener delegates to Query
// with its received context. Referenced argument values remain caller-owned.
func OpenQuery(queryer Queryer, statement string, args ...any) CursorOpener {
	args = append([]any(nil), args...)
	return func(ctx context.Context) (Cursor, error) {
		rows, err := Query(ctx, queryer, statement, args...)
		if err != nil {
			return nil, err
		}
		return rows, nil
	}
}

// EnvelopeOptions selects metadata and optional validation for QueryEnvelopeJSON.
// Its zero value emits {"provenance":null,"data":<Table>}.
type EnvelopeOptions struct {
	// Provenance supplies the default metadata's provenance member.
	Provenance any
	// Header, when non-nil, replaces the default metadata with a JSON object.
	// Its encoded member order is retained; a top-level data member is invalid.
	// Header is encoded once before opening, then frozen. Provenance is ignored
	// when Header is supplied. The completed data member is always appended last.
	Header any
	// Validate runs only after Read has finalized the complete cursor. It may
	// reject the Table, but must not mutate it or retain it for later mutation.
	Validate func(Table) error
	// MaxRows only narrows resolved Limits.MaxRows. Zero adds no extra cap;
	// negative values and values greater than the package maximum are invalid.
	MaxRows int
}

// EnvelopeError identifies the envelope phase while preserving original causes.
// Stage is one of limits, header, budget, query, consume, validate or encode.
// Early input, opener and validation failures retain their original precedence;
// consumption retains Read's finalization joins, and encoding checks context.
type EnvelopeError struct {
	Stage string
	cause error
}

// Error returns a static phase message without echoing metadata or source errors.
func (e *EnvelopeError) Error() string { return "row envelope " + e.Stage + " failed" }

// Unwrap preserves original causes for errors.Is/As and private diagnostics.
func (e *EnvelopeError) Unwrap() error { return e.cause }

// QueryEnvelopeJSON freezes metadata and reserves its exact final JSON overhead
// before opening. It delegates cursor consumption and finalization to Read,
// optionally validates the completed Table, and appends its complete JSON data.
// MaxBytes bounds the entire returned envelope, not only its data member.
//
// The opener receives the original context without a competing watcher. It is
// called only after limit/header/budget checks; its own admission checks retain
// precedence over cancellation. No child context, connection policy, SQL or
// retry is introduced. Every failure returns nil JSON, including close errors,
// incomplete owned cursors, validation, budgets and post-read cancellation.
// Without a validator, ReadJSON retains its existing typed encoding errors.
// The caller must not modify metadata or schema concurrently with the call.
func QueryEnvelopeJSON(ctx context.Context, open CursorOpener, schema []Column, limits Limits, options EnvelopeOptions) (json.RawMessage, error) {
	resolved, err := Resolve(limits)
	if err != nil {
		return nil, &EnvelopeError{Stage: "limits", cause: err}
	}
	if options.MaxRows < 0 || options.MaxRows > MaxRows {
		return nil, &EnvelopeError{Stage: "limits", cause: ErrInvalidRequest}
	}
	if options.MaxRows > 0 && options.MaxRows < resolved.MaxRows {
		resolved.MaxRows = options.MaxRows
	}
	prefix, err := envelopePrefix(options)
	if err != nil {
		return nil, &EnvelopeError{Stage: "header", cause: err}
	}
	budget := resolved
	budget.MaxBytes -= len(prefix) + 1 // Include the final enclosing brace.
	if budget.MaxBytes < 1 {
		return nil, &EnvelopeError{Stage: "budget", cause: ErrCapacity}
	}
	if open == nil {
		return nil, &EnvelopeError{Stage: "query", cause: ErrInvalidRequest}
	}
	rows, err := open(ctx)
	if err != nil {
		if !nilCursor(rows) {
			err = errors.Join(err, rows.Err(), rows.Close(), rows.Err())
		}
		return nil, &EnvelopeError{Stage: "query", cause: err}
	}
	var data json.RawMessage
	if options.Validate == nil {
		data, err = ReadJSON(ctx, rows, schema, budget)
		if err != nil {
			return nil, &EnvelopeError{Stage: "consume", cause: err}
		}
	} else {
		table, readErr := Read(ctx, rows, schema, budget)
		if readErr != nil {
			return nil, &EnvelopeError{Stage: "consume", cause: readErr}
		}
		if err = options.Validate(table); err != nil {
			return nil, &EnvelopeError{Stage: "validate", cause: err}
		}
		if err = ctx.Err(); err != nil {
			return nil, &EnvelopeError{Stage: "encode", cause: err}
		}
		data, err = json.Marshal(table)
		if err != nil {
			return nil, &EnvelopeError{Stage: "encode", cause: err}
		}
	}

	payload := append(prefix, data...)
	payload = append(payload, '}')
	if len(payload) > resolved.MaxBytes {
		err = ErrCapacity
	}
	if err = errors.Join(err, ctx.Err()); err != nil {
		return nil, &EnvelopeError{Stage: "encode", cause: err}
	}
	return json.RawMessage(payload), nil
}

// envelopePrefix freezes metadata instead of serializing a caller callback
// twice. Checking the reserved member lets the data budget remain exact even
// for custom ordered headers or escaped JSON property names.
func envelopePrefix(options EnvelopeOptions) ([]byte, error) {
	metadata := options.Header
	if metadata == nil {
		metadata = struct {
			Provenance any `json:"provenance"`
		}{options.Provenance}
	}
	header, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	var members map[string]json.RawMessage
	if err = json.Unmarshal(header, &members); err != nil {
		return nil, errors.Join(ErrInvalidRequest, err)
	}
	if members == nil {
		return nil, ErrInvalidRequest
	}
	if _, exists := members["data"]; exists {
		return nil, ErrInvalidRequest
	}
	prefix := header[:len(header)-1]
	if len(members) != 0 {
		prefix = append(prefix, ',')
	}
	return append(prefix, `"data":`...), nil
}
