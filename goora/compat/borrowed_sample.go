package compat

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"runtime"
	"runtime/debug"

	"github.com/marcuoli/go-oracle-session/session"
)

// BorrowedSampleOptions supplies facts that database/sql cannot observe.
type BorrowedSampleOptions struct {
	// ExclusiveNonTransaction attests that the caller owns the lease and
	// has no active transaction or competing operation. Only sample reviewed
	// read-only queries that leave no transaction or session changes. Keep
	// that ownership through sampling and finalization.
	ExclusiveNonTransaction bool

	// StandaloneOCI optionally attests that the raw driver is the original
	// standalone godror connection and returns its observed OCI client
	// version. Use connector provenance to establish standalone operation.
	// It runs under sql.Conn.Raw and must only use the supplied driver.
	StandaloneOCI func(driver.Conn) (clientVersion string, ok bool, err error)
}

const qualifiedBorrowedVersion = "23.26.1.0.0"

type borrowedSampleIdentity struct {
	version, user, isDBA, container, databaseRole, openMode, protocol string
}

func (id borrowedSampleIdentity) qualified() bool {
	return id == (borrowedSampleIdentity{
		version: qualifiedBorrowedVersion, user: "SYSTEM", isDBA: "FALSE",
		container: "CDB$ROOT", databaseRole: "PRIMARY", openMode: "READ WRITE", protocol: "tcp",
	})
}

// QualifyBorrowedSample selects a SampleBorrowed finalizer for the same lease.
// It reads identity through the native lease and does not run the caller's
// statement. ErrUnsupported means the provider or deployment is outside the
// qualified profiles. Other errors report admission or metadata failures.
// An eligible caller may keep its native route after an unsupported decision,
// before dispatching the statement. Keep using the original caller context.
//
// Requalification is required for a different physical connection. This check
// covers observed provider profiles and the caller's no-transaction assurance.
// A protocol ping alone cannot establish transaction usability. This function
// adds no cancellation owner. Cancellation after identity dispatch retires
// the lease. In other cases the caller keeps ownership of the supplied lease.
func QualifyBorrowedSample(ctx context.Context, conn *sql.Conn, options BorrowedSampleOptions) (session.SampleFinalizer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if conn == nil || !options.ExclusiveNonTransaction {
		return nil, ErrUnsupported
	}

	var physical driver.Conn
	var ping func(context.Context) error
	var oci bool
	err := conn.Raw(func(raw any) error {
		driverConn, ok := raw.(driver.Conn)
		if !ok || !reflect.TypeOf(driverConn).Comparable() {
			return ErrUnsupported
		}
		physical = driverConn
		if legacy, ok := driverConn.(*legacyDriverConnection); ok {
			if legacy == nil || runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
				return ErrUnsupported
			}
			ping = func(ctx context.Context) error { return PingProtocol(ctx, legacy) }
			return nil
		}
		pinger, ok := driverConn.(driver.Pinger)
		if !ok || options.StandaloneOCI == nil || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" ||
			!nativeGodror(driverConn) || !qualifiedGodrorBuild() {
			return ErrUnsupported
		}
		version, standalone, err := options.StandaloneOCI(driverConn)
		if err != nil {
			return err
		}
		if !standalone || version != qualifiedBorrowedVersion {
			return ErrUnsupported
		}
		oci = true
		ping = pinger.Ping
		return nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, err
	}

	var id borrowedSampleIdentity
	err = conn.QueryRowContext(ctx, "SELECT VERSION_FULL, SYS_CONTEXT('USERENV','SESSION_USER'), "+
		"SYS_CONTEXT('USERENV','ISDBA'), SYS_CONTEXT('USERENV','CON_NAME'), "+
		"DATABASE_ROLE, OPEN_MODE, SYS_CONTEXT('USERENV','NETWORK_PROTOCOL') "+
		"FROM V$INSTANCE CROSS JOIN V$DATABASE").Scan(
		&id.version, &id.user, &id.isDBA, &id.container, &id.databaseRole, &id.openMode, &id.protocol)
	if cause := errors.Join(err, ctx.Err()); errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		// Identity can be the canceled in-flight statement. A normal pool
		// return would leave the session available without settlement proof.
		discardErr := conn.Raw(func(any) error { return driver.ErrBadConn })
		if errors.Is(discardErr, driver.ErrBadConn) || errors.Is(discardErr, sql.ErrConnDone) {
			discardErr = nil
		}
		return nil, errors.Join(cause, discardErr)
	}
	if err != nil {
		if borrowedSampleIdentityUnavailable(err, oci) {
			return nil, ErrUnsupported
		}
		return nil, err
	}
	if !id.qualified() {
		return nil, ErrUnsupported
	}

	return func(ctx context.Context, raw any) (session.SampleHealth, error) {
		if raw != physical {
			return session.SampleHealth{}, session.ErrUnavailable
		}
		if err := ctx.Err(); err != nil {
			return session.SampleHealth{}, err
		}
		if err := ping(ctx); err != nil {
			return session.SampleHealth{}, err
		}
		if err := ctx.Err(); err != nil {
			return session.SampleHealth{}, err
		}
		// These exact profiles have same-lease rollback evidence. The caller
		// excludes an active transaction. Ping contributes protocol evidence.
		return session.SampleHealth{ProtocolSettled: true, TransactionUsable: true}, nil
	}, nil
}

func borrowedSampleIdentityUnavailable(err error, oci bool) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	code, _ := OracleCode(err)
	if oci {
		var native interface{ Code() int }
		if errors.As(err, &native) {
			code = native.Code()
		}
	}
	// Legacy or restricted accounts can lack qualified identity facts.
	switch code {
	case 904, 942, 1031, 2003:
		return true
	}
	return false
}

func nativeGodror(conn driver.Conn) bool {
	t := reflect.TypeOf(conn)
	return t.Kind() == reflect.Pointer && t.Elem().PkgPath() == "github.com/godror/godror" && !reflect.ValueOf(conn).IsNil()
}

func qualifiedGodrorBuild() bool {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return false
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/godror/godror" {
			return dep.Version == "v0.51.0" && dep.Replace == nil
		}
	}
	return false
}
