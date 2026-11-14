package session_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/session"
)

type textConnection struct {
	*executionConn
	seen, prepared []string
}

func (c *textConnection) QueryContext(_ context.Context, text string, _ []driver.NamedValue) (driver.Rows, error) {
	c.seen = append(c.seen, text)
	c.calls.Add(1)
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	return &rows{connection: c.connection}, nil
}
func (c *textConnection) ExecContext(ctx context.Context, text string, args []driver.NamedValue) (driver.Result, error) {
	c.seen = append(c.seen, text)
	return c.executionConn.ExecContext(ctx, text, args)
}
func (c *textConnection) PrepareContext(_ context.Context, text string) (driver.Stmt, error) {
	c.prepared = append(c.prepared, text)
	return &textStatement{c, text}, nil
}

type textStatement struct {
	c    *textConnection
	text string
}

func (*textStatement) NumInput() int                              { return -1 }
func (*textStatement) Close() error                               { return nil }
func (*textStatement) Query([]driver.Value) (driver.Rows, error)  { return nil, session.ErrUnsupported }
func (*textStatement) Exec([]driver.Value) (driver.Result, error) { return nil, session.ErrUnsupported }
func (s *textStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.c.QueryContext(ctx, s.text, args)
}
func (s *textStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.c.ExecContext(ctx, s.text, args)
}

type textConnector struct{ c *textConnection }

func (c textConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (textConnector) Driver() driver.Driver                          { return fakeDriver{} }

type textRunner interface {
	Query(context.Context, session.Statement) (session.Result, error)
	Stream(context.Context, session.Statement, session.StreamOptions) (*session.Stream, error)
	Exec(context.Context, session.Statement, session.ExecOptions) (sql.Result, error)
}

func runTextCommands(t *testing.T, runner textRunner, stmt session.Statement) {
	t.Helper()
	result, err := runner.Query(context.Background(), stmt)
	if err != nil || len(result.Rows) != 1 {
		t.Fatalf("query result changed: %+v %v", result, err)
	}
	cursor, err := runner.Stream(context.Background(), stmt, session.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for cursor.Next() {
		var value string
		if err := cursor.Scan(&value); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if cursor.Err() != nil || !cursor.Complete() || count != 1 {
		t.Fatal("stream did not complete:", cursor.Err())
	}
	resultExec, err := runner.Exec(context.Background(), stmt, session.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := resultExec.RowsAffected(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestStatementTextPolicy(t *testing.T) {
	for _, tc := range []struct {
		name               string
		prepared, preserve bool
	}{
		{"direct/annotated", false, false},
		{"direct/exact", false, true},
		{"prepared/annotated", true, false},
		{"prepared/exact", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &textConnection{executionConn: executionFixture()}
			opts := options(c.connection)
			opts.PreparedContext = tc.prepared
			opts.PreserveStatementText = tc.preserve
			s, err := session.Open(context.Background(), textConnector{c}, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			stmt := statement()
			stmt.Text = "\tSELECT 'λ' AS value /* approved */\r\nFROM dual\n"
			want := stmt.Text
			if !tc.preserve {
				want, err = stmt.Reference.Annotate(stmt.Text)
				if err != nil {
					t.Fatal(err)
				}
			}
			runTextCommands(t, s, stmt)
			tx, err := s.BeginTx(context.Background(), session.TxOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { tx.Rollback(context.Background()) })
			runTextCommands(t, tx, stmt)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(c.seen) != 6 {
				t.Fatal("missing or replayed operation:", c.seen)
			}
			for _, got := range c.seen {
				if got != want {
					t.Fatalf("driver text changed: got %q want %q", got, want)
				}
			}
			wantPrepared := 0
			if tc.prepared {
				wantPrepared = 6
			}
			if len(c.prepared) != wantPrepared {
				t.Fatal("unexpected prepare/fallback:", c.prepared)
			}
			for _, got := range c.prepared {
				if got != want {
					t.Fatalf("prepared text changed: got %q want %q", got, want)
				}
			}
		})
	}
}

func TestStatementTextValidation(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		name := "annotated/"
		if preserve {
			name = "exact/"
		}
		for _, invalid := range []string{"reference", "empty", "oversized"} {
			t.Run(name+invalid, func(t *testing.T) {
				c := &textConnection{executionConn: executionFixture()}
				opts := options(c.connection)
				opts.PreserveStatementText = preserve
				s, err := session.Open(context.Background(), textConnector{c}, opts)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := s.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				stmt := statement()
				switch invalid {
				case "reference":
					stmt = session.Statement{Text: stmt.Text}
				case "empty":
					stmt.Text = ""
				case "oversized":
					stmt.Text = strings.Repeat("x", (1<<20)+1)
				}
				if _, err := s.Query(context.Background(), stmt); err == nil {
					t.Fatal("Query admitted invalid statement")
				}
				if cursor, err := s.Stream(context.Background(), stmt, session.StreamOptions{}); cursor != nil || err == nil {
					t.Fatal("Stream admitted invalid statement")
				}
				if _, err := s.Exec(context.Background(), stmt, session.ExecOptions{}); err == nil {
					t.Fatal("Exec admitted invalid statement")
				}
				if len(c.seen) != 0 || len(c.prepared) != 0 {
					t.Fatal("invalid statement reached driver:", c.seen)
				}
			})
		}
	}
}

func TestStatementTextPool(t *testing.T) {
	c := &textConnection{executionConn: executionFixture()}
	opts := options(c.connection)
	opts.PreserveStatementText = true
	p, err := session.NewPool(context.Background(), session.PoolOptions{
		Size: 1, QueryTimeout: opts.QueryTimeout,
		Open: func(ctx context.Context) (*session.Session, error) { return session.Open(ctx, textConnector{c}, opts) },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	stmt := statement()
	runTextCommands(t, p, stmt)
	if len(c.seen) != 3 {
		t.Fatal("missing/replayed pool operation:", c.seen)
	}
	for _, got := range c.seen {
		if got != stmt.Text {
			t.Fatalf("pool text changed: %q", got)
		}
	}
}

func TestStatementTextBadConnection(t *testing.T) {
	for _, kind := range []string{"query", "stream", "exec"} {
		t.Run(kind, func(t *testing.T) {
			c := &textConnection{executionConn: executionFixture()}
			c.queryErr, c.execErr = driver.ErrBadConn, driver.ErrBadConn
			opts := options(c.connection)
			opts.PreserveStatementText = true
			s, err := session.Open(context.Background(), textConnector{c}, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			stmt := statement()
			invoke := func() error {
				switch kind {
				case "query":
					_, err = s.Query(context.Background(), stmt)
				case "stream":
					_, err = s.Stream(context.Background(), stmt, session.StreamOptions{})
				case "exec":
					_, err = s.Exec(context.Background(), stmt, session.ExecOptions{})
				}
				return err
			}
			err = invoke()
			var projected *session.Error
			if !errors.Is(err, driver.ErrBadConn) || !errors.As(err, &projected) || projected.Reference != stmt.Reference.String() {
				t.Fatal("error/reference identity changed:", err)
			}
			if err := invoke(); !errors.Is(err, session.ErrUnavailable) || len(c.seen) != 1 || c.seen[0] != stmt.Text {
				t.Fatal("exact statement replayed:", err, c.seen)
			}
		})
	}
}
