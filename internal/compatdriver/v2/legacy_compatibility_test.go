package go_ora

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/des"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/network"
)

func TestOwnedCancellationSettlementUsesJoinedConnectionState(t *testing.T) {
	session := network.NewSessionWithInputBufferForDebug(nil)
	for _, test := range []struct {
		name  string
		state ConnectionState
		bad   bool
		want  bool
	}{
		{name: "opened", state: Opened, want: true},
		{name: "closed", state: Closed},
		{name: "bad", state: Opened, bad: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := &Connection{State: test.state, bad: test.bad, session: session}
			if got := connection.CancellationSettled(); got != test.want {
				t.Fatalf("settlement=%v, want %v", got, test.want)
			}
		})
	}
}

func TestOwnedCancellationRejectsAbsentOrInvalidSessions(t *testing.T) {
	type cancellationControl interface {
		BreakCurrentOperation() error
		CancellationSettled() bool
	}
	for _, test := range []struct {
		name       string
		connection *Connection
	}{
		{name: "nil"},
		{name: "closed", connection: &Connection{State: Closed}},
		{name: "uninitialized", connection: &Connection{State: Opened}},
		{name: "bad", connection: &Connection{State: Opened, bad: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			control, ok := any(test.connection).(cancellationControl)
			if !ok {
				t.Fatal("v2 compatibility cancellation hook is unavailable")
			}
			if control.CancellationSettled() {
				t.Fatal("unproven session advertised as settled")
			}
			if err := control.BreakCurrentOperation(); !errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("break without owned session: %v", err)
			}
		})
	}
}

func TestLegacyCancelledOperationsDoNotUseSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &Connection{State: Opened, dBVersion: &DBVersion{Number: 9200}}
	tx := &Transaction{conn: conn, ctx: ctx}
	for _, operation := range []func() error{func() error { return conn.Ping(ctx) }, tx.Commit, tx.Rollback} {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled operation returned %v", err)
		}
		if conn.autoCommit {
			t.Fatal("cancelled transaction changed autocommit")
		}
	}
}

func TestLegacyDriverRejectsUnverifiedOracle8(t *testing.T) {
	for _, major := range []int{8, 9, 10, 19, 26} {
		conn := &Connection{dBVersion: &DBVersion{MajorVersion: major}}
		if (conn.validateSupportedServer() != nil) != (major < 9) {
			t.Fatalf("wrong version policy for %d", major)
		}
	}
}

func TestLegacySessionKeyValidation(t *testing.T) {
	key := []byte("12345678")
	for _, challenge := range []string{"", "0", "zzzzzzzzzzzzzzzz", "0102", "00000000000000000000000000000000"} {
		if _, err := decryptLegacySessionKey(key, challenge); err == nil {
			t.Errorf("accepted malformed challenge %q", challenge)
		}
	}
	if _, err := decryptLegacySessionKey(nil, "0000000000000000"); err == nil {
		t.Fatal("accepted invalid key length")
	}
	block, err := des.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("session!")
	encoded := make([]byte, 8)
	cipher.NewCBCEncrypter(block, make([]byte, 8)).CryptBlocks(encoded, want)
	got, err := decryptLegacySessionKey(key, hex.EncodeToString(encoded))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("session key = %x, %v", got, err)
	}
}

func TestLegacyPasswordPadding(t *testing.T) {
	key := []byte("session!")
	for _, password := range []string{"a", "12345678", "password#with spaces", "caf\u00e9"} {
		got, err := encryptLegacyPassword(password, key)
		if err != nil {
			t.Fatal(err)
		}
		padding := int(got[len(got)-1] - '0')
		if padding != (8-len([]byte(password))%8)%8 {
			t.Fatalf("wrong padding: %s", got)
		}
		encoded, err := hex.DecodeString(got[:len(got)-1])
		if err != nil {
			t.Fatal(err)
		}
		block, _ := des.NewCipher(key)
		decoded := make([]byte, len(encoded))
		cipher.NewCBCDecrypter(block, make([]byte, 8)).CryptBlocks(decoded, encoded)
		if string(decoded[:len(decoded)-padding]) != password {
			t.Fatal("password changed")
		}
		if !bytes.Equal(decoded[len(decoded)-padding:], make([]byte, padding)) {
			t.Fatal("nonzero padding")
		}
	}
	if _, err := encryptLegacyPassword("password", nil); err == nil {
		t.Fatal("accepted invalid key length")
	}
}

func TestLegacyProtocolVersionBoundary(t *testing.T) {
	for _, tt := range []struct {
		version uint16
		legacy  bool
	}{{9200, true}, {10100, true}, {10101, true}, {10102, false}, {19000, false}} {
		conn := &Connection{dBVersion: &DBVersion{Number: tt.version}}
		// DBVersion.Number uses major*1000 + minor*100 + patch, e.g. 9.2.0 is 9200.
		if got := conn.usesLegacyOperations(); got != tt.legacy {
			t.Errorf("%d: %v", tt.version, got)
		}
	}
}

func TestSupportsProtocolPing(t *testing.T) {
	for _, test := range []struct {
		connection *Connection
		want       bool
	}{
		{nil, false},
		{&Connection{}, false},
		{&Connection{dBVersion: &DBVersion{Number: 9208}}, false},
		{&Connection{dBVersion: &DBVersion{Number: 10101}}, false},
		{&Connection{dBVersion: &DBVersion{Number: 10102}}, true},
		{&Connection{dBVersion: &DBVersion{Number: 19000}}, true},
	} {
		if got := test.connection.SupportsProtocolPing(); got != test.want {
			t.Fatalf("SupportsProtocolPing = %t, want %t", got, test.want)
		}
	}
}
