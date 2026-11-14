//go:build integration && settled

package oracle

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/marcuoli/go-oracle-session/session"
)

type nativeSoftConnector struct {
	driver.Connector
	connection nativeSoftConnection
}

func (c *nativeSoftConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	soft, ok := nativeSoftControl(conn)
	if !ok {
		return nil, errors.Join(session.ErrUnsupported, conn.Close())
	}
	c.connection = soft
	return conn, nil
}

func configureNativeCancellation(t *testing.T, cfg nativeConfig, connector driver.Connector, opts session.Options) (driver.Connector, session.Options) {
	t.Helper()
	if !cfg.SettledCancellation {
		return connector, opts
	}
	captured := &nativeSoftConnector{Connector: connector}
	opts.Cancellation = &session.Cancellation{QueryGrace: 5 * time.Second,
		Break: func() error {
			if cfg.proof != nil {
				cfg.proof.requested.Store(true)
			}
			err := captured.connection.BreakCurrentOperation()
			t.Logf("native break joined: type=%T acknowledgement=%t", err, nativeCancellationAcknowledged(err))
			return err
		},
		Reusable: func(err error, requested bool) bool {
			settled := captured.connection.CancellationSettled()
			reusable := settled && (!requested || nativeCancellationAcknowledged(err)) && (err == nil || allErrorLeaves(err, func(leaf error) bool {
				return leaf == context.Canceled || leaf == context.DeadlineExceeded || nativeCancellationAcknowledged(leaf)
			}))
			if cfg.proof != nil {
				cfg.proof.reusable.Store(reusable)
				cfg.proof.checked.Store(true)
			}
			t.Logf("native proof: requested=%t settled=%t acknowledged=%t reusable=%t", requested, settled, nativeCancellationAcknowledged(err), reusable)
			return reusable
		},
	}
	return captured, opts
}

// The fixture function commits a unique event before sleeping. An independent
// observer must see the canceled operation's event. Together with the owned
// break and proof checks, this establishes actual server dispatch.
func nativeSettledCancellation(t *testing.T, cfg nativeConfig, prepared bool) {
	t.Helper()
	if len(cfg.FixtureToken) != 32 {
		t.Fatal("settled profile requires a 32-character fixture marker")
	}
	if cfg.CancellationOutcome != "reuse" && cfg.CancellationOutcome != "retire" && cfg.CancellationOutcome != "qualified" {
		t.Fatal("settled profile requires explicit reuse, retire or native-proof-qualified expectation")
	}
	observer := openSession(t, cfg, false, false)
	for _, kind := range []string{"query", "exec", "stream"} {
		t.Run(kind, func(t *testing.T) {
			proof := &nativeProofState{}
			cfgOperation := cfg
			cfgOperation.proof = proof
			isolateExpectedRetirement := cfg.CancellationOutcome == "retire"
			isolated := openSessionWithBudget(t, cfgOperation, prepared, isolateExpectedRetirement, 500*time.Millisecond)
			identity := scalar(t, isolated, "before-cancel-session", "select sys_context('USERENV','SESSIONID') from dual")
			mode := "direct"
			if prepared {
				mode = "prepared"
			}
			token := cfg.FixtureToken[:12] + "-" + mode + "-" + kind
			eventCount := func() string {
				return scalar(t, observer, "native-event", "select to_char(count(*)) from native_event where token=:token", sql.Named("token", token))
			}
			if eventCount() != "0" {
				t.Fatal("native event was not initially empty")
			}
			type callResult struct {
				err       error
				published bool
			}
			done := make(chan callResult, 1)
			go func() {
				switch kind {
				case "exec":
					result, err := isolated.Exec(context.Background(), statement(t, "native-soft-exec", "declare output varchar2(32); begin output := native_wait(:token); end;", sql.Named("token", token)), session.ExecOptions{})
					done <- callResult{err, result != nil}
				case "query":
					result, err := isolated.Query(context.Background(), statement(t, "native-soft-query", "select native_wait(:token) from dual", sql.Named("token", token)))
					done <- callResult{err, len(result.Rows) != 0}
				case "stream":
					rows, err := isolated.Stream(context.Background(), statement(t, "native-soft-stream", "select native_wait(:token) from dual", sql.Named("token", token)), session.StreamOptions{})
					if err != nil {
						done <- callResult{err, false}
						return
					}
					published := rows.Next() || rows.Complete()
					err = errors.Join(rows.Err(), rows.Close())
					done <- callResult{err, published}
				}
			}()
			observed := false
			deadline := time.Now().Add(4 * time.Second)
			var result callResult
			returned := false
			for time.Now().Before(deadline) {
				if eventCount() == "1" {
					observed = true
					break
				}
				select {
				case result = <-done:
					returned = true
				default:
				}
				if returned {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !returned {
				select {
				case result = <-done:
				case <-time.After(4 * time.Second):
					t.Fatal("native cancellation did not join within its bound")
				}
			}
			if !proof.requested.Load() {
				t.Fatal("configured native break was not requested")
			}
			if cfg.CancellationOutcome == "qualified" && !proof.checked.Load() {
				t.Fatal("native settlement proof was not inspected")
			}
			expectReuse := cfg.CancellationOutcome == "reuse" || cfg.CancellationOutcome == "qualified" && proof.checked.Load() && proof.reusable.Load()
			if !observed || result.published || !errors.Is(result.err, context.DeadlineExceeded) || expectReuse && !nativeCancellationAcknowledged(result.err) {
				requireOK(t, "native soft cancellation", result.err)
				t.Fatal("in-flight dispatch, canceled-result and typed acknowledgement contract failed")
			}
			if !allErrorLeaves(result.err, func(leaf error) bool {
				return leaf == context.DeadlineExceeded || leaf == context.Canceled || nativeCancellationAcknowledged(leaf)
			}) {
				t.Fatal("unexpected native cancellation/cleanup cause")
			}
			if !expectReuse {
				_, err := isolated.Query(context.Background(), statement(t, "retired-native-session", "select 'unavailable' from dual"))
				if !errors.Is(err, session.ErrUnavailable) {
					t.Fatal("unsettled native session was reused")
				}
				replacement := openSession(t, cfg, prepared, false)
				if scalar(t, replacement, "replacement-identity", "select sys_context('USERENV','SESSIONID') from dual") == identity {
					t.Fatal("independent continuation reused the discarded physical session")
				}
				if scalar(t, replacement, "independent-after-retirement", "select 'continued' from dual") != "continued" {
					t.Fatal("independent continuation failed")
				}
				t.Log("server dispatch observed; unproven native health retired, caller opened a fresh session for independent SQL without replay")
				return
			}
			if scalar(t, isolated, "after-cancel-session", "select sys_context('USERENV','SESSIONID') from dual") != identity {
				t.Fatal("cancellation replaced the physical session")
			}
			if scalar(t, isolated, "after-cancel-value", "select 'continued' from dual") != "continued" {
				t.Fatal("settled physical session cannot continue")
			}
			t.Log("server dispatch observed; typed cancellation joined and same physical session continued")
		})
	}
}
