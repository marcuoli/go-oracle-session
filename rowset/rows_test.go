package rowset_test

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/rowset"
	"github.com/marcuoli/go-oracle-session/rowset/internal/testsql"
)

var schema = []rowset.Column{{Name: "ID", Kind: rowset.Integer}, {Name: "VALUE", Kind: rowset.Text}}

func TestReadJSONPreservesNativeMeaningAndOrder(t *testing.T) {
	t.Parallel()
	db, state := testsql.New(t, testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(9007199254740993), []byte("  a,b\n雪  ")}, {nil, nil}, {int64(0), ""}}})
	rows, err := rowset.Query(t.Context(), db, "SELECT fixture")
	if err != nil {
		t.Fatal(err)
	}
	data, err := rowset.ReadJSON(t.Context(), rows, schema, rowset.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "9007199254740993") || state.Closed() != 1 {
		t.Fatal("exact integer or cursor lifecycle lost", string(data))
	}
	var table struct {
		Columns []rowset.Column
		Rows    [][]any
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&table); err != nil {
		t.Fatal(err)
	}
	if len(table.Rows) != 3 || table.Rows[0][0] != json.Number("9007199254740993") || table.Rows[0][1] != "  a,b\n雪  " || table.Rows[1][0] != nil || table.Rows[1][1] != nil || table.Rows[2][0] != json.Number("0") || table.Rows[2][1] != "" {
		t.Fatalf("raw/null/zero/order changed: %#v", table)
	}
}

func TestReadJSONNeverReturnsPrefixOnFailure(t *testing.T) {
	t.Parallel()
	terminal := errors.New("private-terminal")
	closeErr := errors.New("private-close")
	for _, tc := range []struct {
		name   string
		step   testsql.Step
		limits rowset.Limits
		want   error
		cancel bool
	}{
		{"iteration", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}, NextErr: terminal}, rowset.Limits{}, terminal, false},
		{"close", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}, CloseErr: closeErr}, rowset.Limits{}, closeErr, false},
		{"both-standard-library", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}, NextErr: terminal, CloseErr: closeErr}, rowset.Limits{}, terminal, false},
		{"late-type", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}, {int64(2), true}}}, rowset.Limits{}, rowset.ErrInvalidEvidence, false},
		{"row-budget", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}, {int64(2), "second"}}}, rowset.Limits{MaxRows: 1}, rowset.ErrCapacity, false},
		{"byte-budget", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), strings.Repeat("\x00", 100)}}}, rowset.Limits{MaxBytes: 200}, rowset.ErrCapacity, false},
		{"metadata", testsql.Step{Columns: []string{"ID", "WRONG"}, Values: [][]driver.Value{{int64(1), "first"}}}, rowset.Limits{}, rowset.ErrInvalidEvidence, false},
		{"cancellation", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "first"}}}, rowset.Limits{}, context.Canceled, true},
		{"invalid-utf8", testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), []byte{255}}}}, rowset.Limits{}, rowset.ErrInvalidEvidence, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				tc.step.OnNext = func(int) { cancel() }
			}
			db, state := testsql.New(t, tc.step)
			rows, err := rowset.Query(ctx, db, "SELECT fixture")
			if err != nil {
				t.Fatal(err)
			}
			data, err := rowset.ReadJSON(ctx, rows, schema, tc.limits)
			if data != nil || !errors.Is(err, tc.want) || state.Closed() != 1 {
				t.Fatalf("partial success or cause/close lost: %s %v closed%d", data, err, state.Closed())
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("driver error echoed")
			}
		})
	}
}

func TestReadRejectsLossyNumbers(t *testing.T) {
	t.Parallel()
	for name, value := range map[string]driver.Value{"float": float64(1), "nan": math.NaN(), "fraction": "1.5", "overflow": "9223372036854775808"} {
		t.Run(name, func(t *testing.T) {
			db, state := testsql.New(t, testsql.Step{Columns: []string{"ID"}, Values: [][]driver.Value{{value}}})
			rows, err := rowset.Query(t.Context(), db, "SELECT fixture")
			if err != nil {
				t.Fatal(err)
			}
			data, err := rowset.ReadJSON(t.Context(), rows, schema[:1], rowset.Limits{})
			if data != nil || err == nil || state.Closed() != 1 {
				t.Fatal("lossy number accepted")
			}
		})
	}
}

func TestNativeQueryLifetimeAndRequestGuards(t *testing.T) {
	t.Parallel()
	db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"alive"}}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	rows, err := rowset.Query(ctx, db, "SELECT fixture")
	if err != nil || ctx.Err() != nil || !rows.Next() {
		t.Fatal("cursor/context canceled on return", err)
	}
	var value string
	if err := rows.Scan(&value); err != nil || value != "alive" {
		t.Fatal("native Scan changed", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if state.Closed() != 1 {
		t.Fatal("native close missing")
	}
	cancel()
	if rows, err := rowset.Query(ctx, db, "must not run"); rows != nil || !errors.Is(err, context.Canceled) || len(state.Calls()) != 1 {
		t.Fatal("query issued after cancellation")
	}
	for _, limits := range []rowset.Limits{{MaxRows: -1}, {MaxBytes: -1}, {MaxRows: rowset.MaxRows + 1}, {MaxBytes: rowset.MaxBytes + 1}} {
		if _, err := rowset.Resolve(limits); !errors.Is(err, rowset.ErrInvalidRequest) {
			t.Fatal("invalid limits accepted")
		}
	}
	if _, err := rowset.Resolve(rowset.Limits{}); err != nil {
		t.Fatal(err)
	}
}
