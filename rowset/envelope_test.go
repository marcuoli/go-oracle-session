package rowset_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/rowset"
	"github.com/marcuoli/go-oracle-session/rowset/internal/testsql"
	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-sqltemplate/reference"
)

func envelopeStage(t *testing.T, err error, want string) {
	t.Helper()
	stage, ok := errors.AsType[*rowset.EnvelopeError](err)
	if !ok || stage.Stage != want {
		t.Fatalf("stage=%v, want %q", err, want)
	}
	if strings.Contains(err.Error(), "private-") {
		t.Fatalf("cause echoed: %v", err)
	}
}

func TestQueryEnvelopeJSONPreservesMeaningAndOwnership(t *testing.T) {
	t.Parallel()
	const want = `{"provenance":{"source":"fixture"},"data":{"columns":[{"name":"id","kind":"integer"},{"name":"Value","kind":"text"}],"rows":[[9007199254740993,"雪\u003c"],[null,null],[9007199254740993,"雪\u003c"]]}}`
	db, state := testsql.New(t, testsql.Step{Columns: []string{"id", "Value"}, Values: [][]driver.Value{{int64(9007199254740993), "雪<"}, {nil, nil}, {int64(9007199254740993), "雪<"}}})
	calls := 0
	ctx := t.Context()
	open := func(got context.Context) (rowset.Cursor, error) {
		calls++
		if got != ctx {
			t.Fatal("caller context replaced")
		}
		return rowset.Query(got, db, "fixture", "bound")
	}
	validated := false
	options := rowset.EnvelopeOptions{
		Provenance: struct {
			Source string `json:"source"`
		}{"fixture"},
		Validate: func(table rowset.Table) error {
			validated = true
			if state.Closed() != 1 || len(table.Rows) != 3 || table.Rows[0][0] != int64(9007199254740993) || table.Rows[1][0] != nil {
				t.Fatal("validation ran before finalization or values changed", table)
			}
			return nil
		},
	}
	data, err := rowset.QueryEnvelopeJSON(ctx, open, schema, rowset.Limits{MaxBytes: len(want)}, options)
	if err != nil || string(data) != want || calls != 1 || !validated || state.Closed() != 1 {
		t.Fatalf("data=%s err=%v opens=%d validated=%v closes=%d", data, err, calls, validated, state.Closed())
	}
}

func TestQueryEnvelopeJSONCustomHeaderAndBudget(t *testing.T) {
	t.Parallel()
	const want = `{"provenance":"fixture","inferred":true,"data":{"columns":[{"name":"VALUE","kind":"text"}],"rows":[]}}`
	header := struct {
		Provenance string `json:"provenance"`
		Inferred   bool   `json:"inferred"`
	}{"fixture", true}
	for _, tc := range []struct {
		name    string
		bytes   int
		wantErr bool
		opens   int
	}{
		{"exact", len(want), false, 1}, {"short", len(want) - 1, true, 1}, {"overhead-only", 1, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}})
			opens := 0
			open := func(ctx context.Context) (rowset.Cursor, error) { opens++; return rowset.Query(ctx, db, "fixture") }
			data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema[1:], rowset.Limits{MaxBytes: tc.bytes}, rowset.EnvelopeOptions{Header: header})
			if tc.wantErr {
				if data != nil || !errors.Is(err, rowset.ErrCapacity) {
					t.Fatalf("prefix or missing capacity: %s %v", data, err)
				}
				stage := "consume"
				if tc.opens == 0 {
					stage = "budget"
				}
				envelopeStage(t, err, stage)
			} else if err != nil || string(data) != want {
				t.Fatalf("custom envelope changed: %s %v", data, err)
			}
			if opens != tc.opens || state.Closed() != tc.opens {
				t.Fatalf("opens=%d closes=%d", opens, state.Closed())
			}
		})
	}
}

func TestQueryEnvelopeJSONRejectsInvalidHeader(t *testing.T) {
	t.Parallel()
	for name, header := range map[string]any{
		"array": []string{"metadata"}, "scalar": "metadata", "unsupported": make(chan int),
		"null": json.RawMessage("null"), "reserved": map[string]any{"data": nil},
		"escaped-reserved": json.RawMessage(`{"\u0064ata":null}`),
	} {
		t.Run(name, func(t *testing.T) {
			open := func(context.Context) (rowset.Cursor, error) { t.Fatal("invalid header opened query"); return nil, nil }
			data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema, rowset.Limits{}, rowset.EnvelopeOptions{Header: header})
			if data != nil || err == nil {
				t.Fatalf("invalid header accepted: %s %v", data, err)
			}
			envelopeStage(t, err, "header")
		})
	}
}

func TestQueryEnvelopeJSONInputPrecedence(t *testing.T) {
	t.Parallel()
	admission := errors.New("private-admission")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name    string
		limits  rowset.Limits
		options rowset.EnvelopeOptions
		stage   string
		want    error
		opens   int
	}{
		{"limits", rowset.Limits{MaxRows: -1}, rowset.EnvelopeOptions{Header: make(chan int)}, "limits", rowset.ErrInvalidRequest, 0},
		{"source-cap", rowset.Limits{}, rowset.EnvelopeOptions{MaxRows: -1}, "limits", rowset.ErrInvalidRequest, 0},
		{"header", rowset.Limits{}, rowset.EnvelopeOptions{Header: map[string]any{"data": nil}}, "header", rowset.ErrInvalidRequest, 0},
		{"budget", rowset.Limits{MaxBytes: 1}, rowset.EnvelopeOptions{}, "budget", rowset.ErrCapacity, 0},
		{"lazy-admission", rowset.Limits{}, rowset.EnvelopeOptions{}, "query", admission, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			open := func(got context.Context) (rowset.Cursor, error) {
				opens++
				if got != ctx {
					t.Fatal("context changed")
				}
				return nil, admission
			}
			data, err := rowset.QueryEnvelopeJSON(ctx, open, schema, tc.limits, tc.options)
			if data != nil || !errors.Is(err, tc.want) || errors.Is(err, context.Canceled) || opens != tc.opens {
				t.Fatalf("input precedence lost: %s %v opens%d", data, err, opens)
			}
			envelopeStage(t, err, tc.stage)
		})
	}
}

func TestQueryEnvelopeJSONFailuresNeverReturnPrefix(t *testing.T) {
	t.Parallel()
	terminal := errors.New("private-terminal")
	closeErr := errors.New("private-close")
	for _, tc := range []struct {
		name    string
		step    testsql.Step
		limits  rowset.Limits
		options rowset.EnvelopeOptions
		want    error
	}{
		{"iteration", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}, NextErr: terminal}, rowset.Limits{}, rowset.EnvelopeOptions{}, terminal},
		{"close", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}, CloseErr: closeErr}, rowset.Limits{}, rowset.EnvelopeOptions{}, closeErr},
		{"value", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), true}}}, rowset.Limits{}, rowset.EnvelopeOptions{}, rowset.ErrInvalidEvidence},
		{"metadata", testsql.Step{Columns: []string{"ID", "WRONG"}}, rowset.Limits{}, rowset.EnvelopeOptions{}, rowset.ErrInvalidEvidence},
		{"source-row-cap", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}, {int64(2), "second"}}}, rowset.Limits{MaxRows: 10}, rowset.EnvelopeOptions{MaxRows: 1}, rowset.ErrCapacity},
		{"cannot-loosen-cap", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}, {int64(2), "second"}}}, rowset.Limits{MaxRows: 1}, rowset.EnvelopeOptions{MaxRows: 2}, rowset.ErrCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, state := testsql.New(t, tc.step)
			data, err := rowset.QueryEnvelopeJSON(t.Context(), rowset.OpenQuery(db, "fixture"), schema, tc.limits, tc.options)
			if data != nil || !errors.Is(err, tc.want) || state.Closed() != 1 {
				t.Fatalf("prefix/cause/lifetime: %s %v closed%d", data, err, state.Closed())
			}
			envelopeStage(t, err, "consume")
		})
	}
}

type failedOpenCursor struct {
	closes             int
	terminal, closeErr error
}

func (*failedOpenCursor) Columns() ([]string, error) { panic("failed opener cursor consumed") }
func (*failedOpenCursor) Next() bool                 { panic("failed opener cursor consumed") }
func (*failedOpenCursor) Scan(...any) error          { panic("failed opener cursor consumed") }
func (c *failedOpenCursor) Err() error               { return c.terminal }
func (c *failedOpenCursor) Close() error             { c.closes++; return c.closeErr }

func TestQueryEnvelopeJSONFinalizesFailedOpen(t *testing.T) {
	t.Parallel()
	queryErr, terminal, closeErr := errors.New("private-query"), errors.New("private-terminal"), errors.New("private-close")
	cursor := &failedOpenCursor{terminal: terminal, closeErr: closeErr}
	open := func(context.Context) (rowset.Cursor, error) { return cursor, queryErr }
	data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema, rowset.Limits{}, rowset.EnvelopeOptions{})
	if data != nil || cursor.closes != 1 {
		t.Fatalf("failed opener leaked: %s %v closes%d", data, err, cursor.closes)
	}
	for _, want := range []error{queryErr, terminal, closeErr} {
		if !errors.Is(err, want) {
			t.Fatalf("cause lost: %v", err)
		}
	}
	envelopeStage(t, err, "query")
	var absent *sql.Rows
	data, err = rowset.QueryEnvelopeJSON(t.Context(), func(context.Context) (rowset.Cursor, error) { return absent, queryErr }, schema, rowset.Limits{}, rowset.EnvelopeOptions{})
	if data != nil || !errors.Is(err, queryErr) {
		t.Fatalf("typed nil cursor error lost: %s %v", data, err)
	}
}

func TestQueryEnvelopeJSONValidationAndCancellation(t *testing.T) {
	t.Parallel()
	domainErr := errors.New("private-domain")
	for _, tc := range []struct {
		name       string
		validation error
		cancel     bool
		want       error
		stage      string
	}{
		{"domain", domainErr, false, domainErr, "validate"},
		{"domain-before-late-cancel", domainErr, true, domainErr, "validate"},
		{"late-cancel", nil, true, context.Canceled, "encode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"complete"}}})
			options := rowset.EnvelopeOptions{Validate: func(rowset.Table) error {
				if state.Closed() != 1 {
					t.Fatal("validation before close")
				}
				if tc.cancel {
					cancel()
				}
				return tc.validation
			}}
			data, err := rowset.QueryEnvelopeJSON(ctx, rowset.OpenQuery(db, "fixture"), schema[1:], rowset.Limits{}, options)
			if data != nil || !errors.Is(err, tc.want) || state.Closed() != 1 {
				t.Fatalf("failure became JSON: %s %v", data, err)
			}
			if tc.validation != nil && errors.Is(err, context.Canceled) {
				t.Fatal("validation input precedence changed")
			}
			envelopeStage(t, err, tc.stage)
		})
	}
}

func TestOpenQueryIsLazyAndCapturesArguments(t *testing.T) {
	t.Parallel()
	db, state := testsql.New(t, testsql.Step{SQL: "fixture", Columns: []string{"VALUE"}, Values: [][]driver.Value{{"exact"}}})
	args := []any{"original"}
	open := rowset.OpenQuery(db, "fixture", args...)
	args[0] = "changed"
	if len(state.Calls()) != 0 {
		t.Fatal("factory queried eagerly")
	}
	data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{})
	if err != nil || !json.Valid(data) || state.Closed() != 1 || len(state.Calls()) != 1 || state.Calls()[0].Args[0].Value != "original" {
		t.Fatalf("lazy query/arguments lost: %s %v %+v", data, err, state.Calls())
	}
	if data, err := rowset.QueryEnvelopeJSON(t.Context(), nil, schema, rowset.Limits{}, rowset.EnvelopeOptions{}); data != nil || !errors.Is(err, rowset.ErrInvalidRequest) {
		t.Fatalf("nil opener accepted: %s %v", data, err)
	}
}

func TestQueryEnvelopeJSONHonorsOwnedCursorCompletion(t *testing.T) {
	t.Parallel()
	db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"prefix"}}})
	open := func(ctx context.Context) (rowset.Cursor, error) {
		rows, err := rowset.Query(ctx, db, "fixture")
		if err != nil {
			return nil, err
		}
		return &incompleteCursor{Rows: rows}, nil
	}
	data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{})
	if data != nil || !errors.Is(err, rowset.ErrInvalidEvidence) || state.Closed() != 1 {
		t.Fatalf("incomplete owned cursor became success: %s %v", data, err)
	}
}

func TestQueryEnvelopeJSONEmptyHeaderAndNestedData(t *testing.T) {
	t.Parallel()
	for _, header := range []any{struct{}{}, map[string]any{"details": map[string]any{"data": "metadata"}}} {
		db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}})
		data, err := rowset.QueryEnvelopeJSON(t.Context(), rowset.OpenQuery(db, "fixture"), schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{Header: header})
		if err != nil || !json.Valid(data) || state.Closed() != 1 {
			t.Fatalf("valid metadata rejected: %s %v", data, err)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			t.Fatal(err)
		}
		if string(object["data"]) != `{"columns":[{"name":"VALUE","kind":"text"}],"rows":[]}` {
			t.Fatalf("table missing: %s", data)
		}
	}
}

type envelopeScanFailure struct {
	rowset.Cursor
	cause error
}

func (c envelopeScanFailure) Scan(...any) error { return c.cause }

func TestQueryEnvelopeJSONScanAndReadCancellation(t *testing.T) {
	t.Parallel()
	scanErr := errors.New("private-scan")
	for _, cancelRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		step := testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"prefix"}}}
		if cancelRead {
			step.OnNext = func(int) { cancel() }
		}
		db, state := testsql.New(t, step)
		open := func(ctx context.Context) (rowset.Cursor, error) {
			rows, err := rowset.Query(ctx, db, "fixture")
			if err != nil {
				return nil, err
			}
			if cancelRead {
				return rows, nil
			}
			return envelopeScanFailure{Cursor: rows, cause: scanErr}, nil
		}
		validate := func(rowset.Table) error { t.Fatal("validation after failed consumption"); return nil }
		data, err := rowset.QueryEnvelopeJSON(ctx, open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{Validate: validate})
		cancel()
		want := scanErr
		if cancelRead {
			want = context.Canceled
		}
		if data != nil || !errors.Is(err, want) || state.Closed() != 1 {
			t.Fatalf("read failure lost: %s %v closes%d", data, err, state.Closed())
		}
		envelopeStage(t, err, "consume")
	}
}

type frozenEnvelopeHeader struct{ calls, opens *int }

func (h frozenEnvelopeHeader) MarshalJSON() ([]byte, error) {
	*h.calls++
	if *h.calls != 1 || *h.opens != 0 {
		return nil, errors.New("private-metadata-reencoded")
	}
	return []byte(`{"label":"frozen"}`), nil
}

func TestQueryEnvelopeJSONFreezesMetadataBeforeOpening(t *testing.T) {
	t.Parallel()
	db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}})
	calls, opens := 0, 0
	open := func(ctx context.Context) (rowset.Cursor, error) { opens++; return rowset.Query(ctx, db, "fixture") }
	data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{Header: frozenEnvelopeHeader{&calls, &opens}})
	const want = `{"label":"frozen","data":{"columns":[{"name":"VALUE","kind":"text"}],"rows":[]}}`
	if err != nil || string(data) != want || calls != 1 || opens != 1 || state.Closed() != 1 {
		t.Fatalf("metadata/open order changed: %s %v metadata%d opens%d", data, err, calls, opens)
	}
}

func TestQueryEnvelopeJSONReleasesOwnedStreamForContinuation(t *testing.T) {
	connector, state := testsql.NewConnector(
		testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"stream"}}},
		testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"continuation"}}},
	)
	owned, err := session.Open(t.Context(), connector, session.Options{ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second, MaxRows: 8, MaxBytes: 64 << 10, Dispose: func() error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := owned.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	ref, err := reference.NewReference("envelope", "fixture", "stream", 1)
	if err != nil {
		t.Fatal(err)
	}
	statement := session.Statement{Reference: ref, Text: "SELECT fixture"}
	var stream *session.Stream
	open := func(ctx context.Context) (rowset.Cursor, error) {
		var err error
		stream, err = owned.Stream(ctx, statement, session.StreamOptions{})
		return stream, err
	}
	data, err := rowset.QueryEnvelopeJSON(t.Context(), open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{})
	if err != nil || !stream.Complete() || state.Closed() != 1 || !strings.Contains(string(data), `"rows":[["stream"]]`) {
		t.Fatalf("owned completion failed: %s %v", data, err)
	}
	next, err := owned.Query(t.Context(), statement)
	if err != nil || len(next.Rows) != 1 || next.Rows[0][0].Text != "continuation" || state.Closed() != 2 || len(state.Calls()) != 2 {
		t.Fatalf("owned lease not released for continuation: %+v %v", next, err)
	}
}

// Complete is checked after Read's final context snapshot. Canceling here
// deterministically represents cancellation between Read and ReadJSON encoding.
type cancelAfterReadCursor struct {
	rowset.Cursor
	cancel context.CancelFunc
}

func (c cancelAfterReadCursor) Complete() bool { c.cancel(); return true }

func TestQueryEnvelopeJSONPreservesReadJSONPostReadEncodeError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"complete"}}})
	open := func(ctx context.Context) (rowset.Cursor, error) {
		rows, err := rowset.Query(ctx, db, "fixture")
		if err != nil {
			return nil, err
		}
		return cancelAfterReadCursor{Cursor: rows, cancel: cancel}, nil
	}
	data, err := rowset.QueryEnvelopeJSON(ctx, open, schema[1:], rowset.Limits{}, rowset.EnvelopeOptions{})
	if data != nil || !errors.Is(err, context.Canceled) || state.Closed() != 1 {
		t.Fatalf("post-read cancel returned JSON: %s %v", data, err)
	}
	envelopeStage(t, err, "consume")
	native, ok := errors.AsType[*rowset.Error](err)
	if !ok || native.Operation != "encode" {
		t.Fatalf("existing ReadJSON typed encode error lost: %v", err)
	}
}

func TestOpenQueryFailureReturnsNilCursor(t *testing.T) {
	queryErr := errors.New("private-query")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	db, state := testsql.New(t, testsql.Step{QueryErr: queryErr})
	for _, tc := range []struct {
		name string
		open rowset.CursorOpener
		ctx  context.Context
		want error
	}{
		{"nil-handle", rowset.OpenQuery(nil, "fixture"), t.Context(), rowset.ErrInvalidRequest},
		{"query", rowset.OpenQuery(db, "fixture"), t.Context(), queryErr},
		{"canceled", rowset.OpenQuery(db, "fixture"), ctx, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cursor, err := tc.open(tc.ctx)
			if cursor != nil || !errors.Is(err, tc.want) {
				t.Fatalf("failed opener returned non-nil interface: cursor=%#v err=%v", cursor, err)
			}
		})
	}
	if len(state.Calls()) != 1 {
		t.Fatalf("canceled opener queried: %+v", state.Calls())
	}
}
