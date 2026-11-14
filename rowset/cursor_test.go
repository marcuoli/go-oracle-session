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

type incompleteCursor struct{ *sql.Rows }

func (*incompleteCursor) Complete() bool { return false }

func TestReadJSONRejectsIncompleteCursorAndTypedNil(t *testing.T) {
	db, state := testsql.New(t, testsql.Step{Columns: []string{"ID"}, Values: [][]driver.Value{{int64(1)}}})
	rows, err := rowset.Query(t.Context(), db, "SELECT fixture")
	if err != nil {
		t.Fatal(err)
	}
	data, err := rowset.ReadJSON(t.Context(), &incompleteCursor{Rows: rows}, schema[:1], rowset.Limits{})
	if data != nil || !errors.Is(err, rowset.ErrInvalidEvidence) || state.Closed() != 1 {
		t.Fatal("incomplete cursor became successful JSON", err)
	}
	var absent *sql.Rows
	data, err = rowset.ReadJSON(t.Context(), absent, schema[:1], rowset.Limits{})
	if data != nil || !errors.Is(err, rowset.ErrInvalidRequest) {
		t.Fatal("typed nil cursor accepted", err)
	}
}

func TestOwnedStreamJSONCompletesBeforeSessionContinuation(t *testing.T) {
	connector, state := testsql.NewConnector(
		testsql.Step{Columns: []string{"ID", "VALUE"}, Values: [][]driver.Value{{int64(1), "exact"}, {int64(2), nil}}},
		testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"next operation"}}},
	)
	owned, err := session.Open(t.Context(), connector, session.Options{
		ConnectTimeout: time.Second, QueryTimeout: time.Second, CleanupTimeout: time.Second,
		MaxRows: 8, MaxBytes: 64 << 10, Dispose: func() error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := owned.Close(ctx); err != nil {
			t.Error("owned session cleanup failed", err)
		}
	})
	ref, err := reference.NewReference("stream", "json", "complete", 1)
	if err != nil {
		t.Fatal(err)
	}
	statement := session.Statement{Reference: ref, Text: "SELECT fixture"}
	cursor, err := owned.Stream(t.Context(), statement, session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := rowset.ReadJSON(t.Context(), cursor, schema, rowset.Limits{})
	if err != nil || !cursor.Complete() || state.Closed() != 1 {
		t.Fatal("owned stream JSON did not join successful EOF", err)
	}
	var decoded struct{ Rows [][]json.RawMessage }
	if err := json.Unmarshal(data, &decoded); err != nil || len(decoded.Rows) != 2 ||
		string(decoded.Rows[0][0]) != "1" || string(decoded.Rows[0][1]) != "\"exact\"" ||
		string(decoded.Rows[1][1]) != "null" {
		t.Fatal("owned stream values/NULL lost", err)
	}
	next, err := owned.Query(t.Context(), statement)
	if err != nil || len(next.Rows) != 1 || next.Rows[0][0].Text != "next operation" ||
		state.Closed() != 2 || len(state.Calls()) != 2 {
		t.Fatal("JSON did not return the existing session lease", err)
	}
}

func TestQueryJSONBindsArgumentsAndValidatesLimitsBeforeSQL(t *testing.T) {
	db, state := testsql.New(t, testsql.Step{Columns: []string{"VALUE"}, Values: [][]driver.Value{{"bound"}}})
	data, err := rowset.QueryJSON(t.Context(), db, "SELECT :1 AS VALUE", schema[1:], rowset.Limits{}, "bound")
	if err != nil || !strings.Contains(string(data), "\"rows\":[[\"bound\"]]") {
		t.Fatal("native QueryJSON did not preserve bound data", err)
	}
	calls := state.Calls()
	if len(calls) != 1 || len(calls[0].Args) != 1 || calls[0].Args[0].Value != "bound" || state.Closed() != 1 {
		t.Fatal("native QueryJSON changed arguments or finalization")
	}
	data, err = rowset.QueryJSON(t.Context(), db, "must not execute", schema, rowset.Limits{MaxRows: -1})
	if data != nil || !errors.Is(err, rowset.ErrInvalidRequest) || len(state.Calls()) != 1 {
		t.Fatal("invalid JSON limit executed SQL", err)
	}
}
