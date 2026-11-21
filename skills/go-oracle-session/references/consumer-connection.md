# Pure-Go consumer connection wiring

Use the same SDK connector in every ordinary pure-Go Oracle entry point. In a
new consumer, put the wiring in `internal/oracle/connect.go` and its
regressions in `internal/oracle/connect_test.go`. When an existing package owns
the connection, use its current location. Route ordinary opens, explicit
leases, and administrative pure-Go connections through the same construction
point. Keep an explicitly selected OCI route independent.

| SDK file | Import suffix | Use |
| --- | --- | --- |
| `goora/compat/connector.go` | `/goora/compat` | `NewConnector(dsn, options)`. Use `NewConnectorWithTLSConfig(dsn, options, config)` when a TCPS caller already supplies a `*tls.Config`. |
| `goora/compat/policy.go` | `/goora/compat` | Leave `Options.Mode` unset so the SDK owns its default. Supply `Options.Dialer` for existing transport control and `Options.LoginWarningHandler` for an existing concurrent-safe warning callback. |
| `transport/dialer.go` | `/transport` | `New(timeout)` creates one transport for a physical session and its redirects. Use `WithDialer(timeout, dial)` if the caller needs to keep an existing dialing function. Use its `Close` as the disposal hook. |
| `session/session.go` | `/session` | `Open(ctx, connector, options)` owns one lease. Use `ConnectTimeout`, `QueryTimeout`, `CleanupTimeout`, `MaxRows`, `MaxBytes` and `Dispose`. Close the session with `Close(ctx)`. |
| `goora/compat/connection_diagnostics.go` | `/goora/compat` | `ConnectWithDiagnostics(ctx)` returns attempt facts. `ConnectObserved(ctx, observer)` runs synchronous `ConnectionObserver.Before` and `.After` when recording must gate admission. Both use the same SDK policy. |
| `diagnostic/connection.go` | `/diagnostic` | `Connection(err, secrets...)` extracts redacted connection messages. The caller owns what it presents and persists. |
| `goora/compat/protocol_ping.go` | `/goora/compat` | `PingProtocol(ctx, conn)` performs a SQL-free native probe on the exclusively owned original `driver.Conn`. Use only where the caller already needs that proof. Treat `compat.ErrUnsupported` as missing evidence. |

All import suffixes are relative to `github.com/marcuoli/go-oracle-session`.
For an existing database/sql pool, pass the connector to `sql.OpenDB`. The
pool owner remains responsible for bounds, close, and its existing transport
lifetime. A single session transport must not be shared by independent leases.
For a bounded session, use this wiring with the application's existing limits.
The example's options supply bounds and no existing disposal hook. If migrating
an owner with additional disposal work, keep that work in the sole disposal
hook and make it idempotent and joined.

~~~go
package oracle

import (
	"context"
	"errors"

	"github.com/marcuoli/go-oracle-session/goora/compat"
	"github.com/marcuoli/go-oracle-session/session"
	"github.com/marcuoli/go-oracle-session/transport"
)

func openSession(ctx context.Context, dsn string, options session.Options) (*session.Session, error) {
	dialer, err := transport.New(options.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	connector, err := compat.NewConnector(dsn, compat.Options{Dialer: dialer})
	if err != nil {
		return nil, errors.Join(err, dialer.Close())
	}
	options.Dispose = dialer.Close
	leased, err := session.Open(ctx, connector, options)
	if err != nil {
		return nil, errors.Join(err, dialer.Close())
	}
	return leased, nil
}
~~~

The session path owns cancellation and transport disposal. Keep
`Options.Cancellation` unset unless the caller has qualified native
settlement and reuse for that configuration. Avoid a second cancellation
watcher. Retire the physical connection if a native protocol probe fails or
is canceled. A successful probe does not prove transaction usability. Use `compat.Out`, `compat.Number` and the
shared LOB types where migration needs driver-neutral values. Keep DSN and role
translation, TLS policy, SQL, endpoint admission and display in the application.
Remove live app selection once parity is verified. Keep any separately
authorized retained source and its retirement gate.

`NewConnector` with the SDK default is available in v0.16.0. Verify optional
symbols against the consumer's exact pinned module. A first-party workspace can
preview changes during development. For integration, prove module identity and
sums, build and test with `GOWORK=off` and no local replacement, and inspect the
actual binary. Verify every production pure-Go entry point and preserve its
admission, cancellation, error, and cleanup behavior on qualified targets.
