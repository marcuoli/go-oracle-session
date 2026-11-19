package diagnostic_test

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/marcuoli/go-oracle-session/diagnostic"
)

func TestConnectionClassifiesWrappedCausesAndPreservesNativeCodes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		kind    diagnostic.ConnectionKind
		code    string
	}{
		{"oracle", errors.New("ORA-01017: invalid login"), diagnostic.ConnectionOracle, "ORA-01017"},
		{"tns", errors.New("TNS-12514: unknown service"), diagnostic.ConnectionTNS, "TNS-12514"},
		{"dns", &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Name: "db.example.invalid", Err: "no such host", IsNotFound: true}}, diagnostic.ConnectionDNS, ""},
		{"tcp", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, diagnostic.ConnectionTCP, ""},
		{"tls", x509.UnknownAuthorityError{}, diagnostic.ConnectionTLS, ""},
		{"timeout", fmt.Errorf("open: %w", context.DeadlineExceeded), diagnostic.ConnectionTimeout, ""},
		{"canceled", context.Canceled, diagnostic.ConnectionCanceled, ""},
		{"unknown", errors.New("unsupported negotiation"), diagnostic.ConnectionUnknown, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.Connection(tc.failure)
			if err != nil || len(got) != 1 || got[0].Kind != tc.kind || got[0].Code != tc.code || got[0].Text == "" {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestConnectionReusesCredentialRedactionWithoutMutatingInputs(t *testing.T) {
	secret := "my/private pass?&"
	secrets := []string{secret}
	original := "ORA-01017: invalid login password='other' oracle://reader:other@host/db token=value authorization=Bearer value\nTNS-12514: unknown service " + secret + " " + url.QueryEscape(secret) + " " + url.PathEscape(secret) + "\x1b[31m"
	failure := fmt.Errorf("private wrapper: %w", errors.New(original))
	got, err := diagnostic.Connection(failure, secrets...)
	if err != nil || len(got) != 2 || got[0].Code != "ORA-01017" || got[1].Code != "TNS-12514" {
		t.Fatal(got, err)
	}
	for _, message := range got {
		for _, value := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), "reader:other", "Bearer value", "\x1b", "private wrapper"} {
			if strings.Contains(message.Text, value) {
				t.Fatal("private content remained", value)
			}
		}
	}
	if secrets[0] != secret || !strings.Contains(failure.Error(), original) {
		t.Fatal("input changed")
	}
	again, err := diagnostic.Connection(errors.New(got[0].Text), "ORA")
	if err != nil || len(again) != 1 || again[0].Code != "ORA-01017" {
		t.Fatal(again, err)
	}
}

type unsafeTimeoutCycle struct{}

func (*unsafeTimeoutCycle) Error() string   { panic("wrapper Error must not be formatted") }
func (e *unsafeTimeoutCycle) Unwrap() error { return e }
func (*unsafeTimeoutCycle) Timeout() bool   { panic("wrapper Timeout must not recurse") }
func (*unsafeTimeoutCycle) Temporary() bool { return false }

func TestConnectionBoundsGraphsMessagesAndAggregateSeparators(t *testing.T) {
	for _, failure := range []error{&unsafeTimeoutCycle{}, errors.New(strings.Repeat("x", diagnostic.MaxBytes+1)), errors.Join(errors.New(strings.Repeat("a", diagnostic.MaxBytes/2)), errors.New(strings.Repeat("b", diagnostic.MaxBytes/2)))} {
		got, err := diagnostic.Connection(failure)
		if !errors.Is(err, diagnostic.ErrCapacity) || len(got) != 0 {
			t.Fatalf("partial/unbounded result %+v %v", got, err)
		}
	}
	var failures []error
	for i := 0; i < 33; i++ {
		failures = append(failures, fmt.Errorf("unique failure %d", i))
	}
	if got, err := diagnostic.Connection(errors.Join(failures...)); !errors.Is(err, diagnostic.ErrCapacity) || len(got) != 0 {
		t.Fatal(got, err)
	}
	got, err := diagnostic.Connection(errors.Join(errors.New(strings.Repeat("a", diagnostic.MaxBytes/2-1)), errors.New(strings.Repeat("b", diagnostic.MaxBytes/2))))
	if err != nil || len(got) != 2 || len(got[0].Text)+len(got[1].Text)+1 != diagnostic.MaxBytes {
		t.Fatal("exact bound", len(got), err)
	}
	if got, err := diagnostic.Connection(errors.New(string([]byte{255}))); err != nil || len(got) != 0 {
		t.Fatal("invalid UTF8 admitted", got, err)
	}
}

func TestConnectionPreservesOrderDeduplicatesAndIsConcurrent(t *testing.T) {
	failure := errors.Join(errors.New("ORA-01017: invalid login"), errors.New("TNS-12514: unknown service"), errors.New("ORA-01017: invalid login"))
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Go(func() {
			got, err := diagnostic.Connection(failure)
			if err != nil || len(got) != 2 || got[0].Kind != diagnostic.ConnectionOracle || got[1].Kind != diagnostic.ConnectionTNS {
				t.Error(got, err)
			}
		})
	}
	group.Wait()
}
