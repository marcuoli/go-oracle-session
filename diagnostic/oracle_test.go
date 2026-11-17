package diagnostic_test

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/marcuoli/go-oracle-session/diagnostic"
)

func TestOracleWrappedStackRedactionAndOwnership(t *testing.T) {
	secrets := []string{"my/private pass"}
	raw := errors.New("ORA-00604: error at recursive SQL level 1\r\nORA-01219: database not open\npassword='private' oracle://reader:private@host/db\nvalue=my/private pass")
	input := fmt.Errorf("outer password=wrapper: %w", errors.Join(raw, errors.New("ORA-01031: insufficient privileges")))
	got, err := diagnostic.Oracle(input, secrets...)
	if err != nil || len(got) != 3 {
		t.Fatal(got, err)
	}
	if got[0].Code != "ORA-00604" || got[0].Text != "ORA-00604: error at recursive SQL level 1" || got[2].Text != "ORA-01031: insufficient privileges" {
		t.Fatal(got)
	}
	if got[1].Code != "ORA-01219" || !strings.Contains(got[1].Text, "database not open") || !strings.Contains(got[1].Text, "[REDACTED]") || strings.Contains(got[1].Text, "private") || strings.Contains(got[1].Text, "reader") {
		t.Fatal(got[1])
	}
	if secrets[0] != "my/private pass" || !strings.Contains(raw.Error(), "password='private'") {
		t.Fatal("input mutated")
	}
}

type cycle struct{}

func (*cycle) Error() string   { return "ORA-01219: message" }
func (e *cycle) Unwrap() error { return e }
func TestOracleRejectsIncompleteAndUnboundedDiagnostics(t *testing.T) {
	for _, err := range []error{&cycle{}, errors.New("ORA-01219: " + strings.Repeat("x", diagnostic.MaxBytes)), errors.New(strings.Repeat("ORA-01031: failure\n", 33))} {
		got, failure := diagnostic.Oracle(err)
		// Repeated identical messages are de-duplicated, rather than a capacity error.
		if strings.Count(err.Error(), "ORA-01031") == 33 {
			if failure != nil || len(got) != 1 {
				t.Fatal(got, failure)
			}
		} else if !errors.Is(failure, diagnostic.ErrCapacity) || len(got) != 0 {
			t.Fatal(got, failure)
		}
	}
	for _, err := range []error{nil, errors.New("password=secret"), errors.New("query failed (ORA-01219)"), errors.New("ORA-01219:")} {
		got, failure := diagnostic.Oracle(err)
		if failure != nil || len(got) != 0 {
			t.Fatal(got, failure)
		}
	}
}

func TestOracleRedactionIsIdempotentAndPreservesCode(t *testing.T) {
	got, err := diagnostic.Oracle(errors.New("ORA-01219: password=secret"), "ORA")
	if err != nil || len(got) != 1 || got[0].Code != "ORA-01219" || got[0].Text != "ORA-01219: password=[REDACTED]" {
		t.Fatal(got, err)
	}
	again, err := diagnostic.Oracle(errors.New(got[0].Text))
	if err != nil || len(again) != 1 || again[0] != got[0] {
		t.Fatal(again, err)
	}
	unique := ""
	for i := 0; i < 33; i++ {
		unique += fmt.Sprintf("ORA-01031: failure %d\n", i)
	}
	if got, err := diagnostic.Oracle(errors.New(unique)); !errors.Is(err, diagnostic.ErrCapacity) || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestOracleStructuralRedactionPrecedesKnownSecrets(t *testing.T) {
	for _, tc := range []struct {
		name, input, secret, want string
	}{
		{"URL scheme", "ORA-20000: oracle://reader:private@host/db", "oracle", "ORA-20000: [REDACTED CONNECTION]"},
		{"assignment key", "ORA-20000: password=private", "password", "ORA-20000: [REDACTED]=[REDACTED]"},
		{"Bearer authorization", "ORA-20000: Authorization: Bearer abcdef", "", "ORA-20000: Authorization=[REDACTED]"},
		{"Basic authorization", "ORA-20000: Authorization: Basic YWJjOmRlZg==", "", "ORA-20000: Authorization=[REDACTED]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.Oracle(errors.New(tc.input), tc.secret)
			want := []diagnostic.Message{{Code: "ORA-20000", Text: tc.want}}
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("Oracle() = %#v, %v; want %#v", got, err, want)
			}
		})
	}
}

type typedOracleCause struct{ text string }

func (e *typedOracleCause) Error() string { return e.text }

type emptyMulti struct{ children []error }

func (e emptyMulti) Error() string   { return "ORA-01219: message" }
func (e emptyMulti) Unwrap() []error { return e.children }

type emptySingle struct{}

func (emptySingle) Error() string { return "ORA-01219: message" }
func (emptySingle) Unwrap() error { return nil }

func TestOracleCompleteTextAndErrorOwnership(t *testing.T) {
	secrets := []string{"pass", "my/private pass"}
	originalSecrets := slices.Clone(secrets)
	rawText := "ORA-00604: error at recursive SQL level 1\r\nORA-01219: database not open\npassword='private' oracle://reader:private@host/db\nvalue=my/private pass"
	raw := &typedOracleCause{rawText}
	input := fmt.Errorf("outer password=wrapper: %w", errors.Join(raw, errors.New("ORA-01031: insufficient privileges")))
	want := []diagnostic.Message{
		{Code: "ORA-00604", Text: "ORA-00604: error at recursive SQL level 1"},
		{Code: "ORA-01219", Text: "ORA-01219: database not open\n[REDACTED]word=[REDACTED] [REDACTED CONNECTION]\nvalue=[REDACTED]"},
		{Code: "ORA-01031", Text: "ORA-01031: insufficient privileges"},
	}
	got, err := diagnostic.Oracle(input, secrets...)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Oracle() = %#v, %v; want %#v", got, err, want)
	}
	var typed *typedOracleCause
	if !slices.Equal(secrets, originalSecrets) || raw.Error() != rawText || !errors.Is(input, raw) || !errors.As(input, &typed) || typed != raw {
		t.Fatal("input, secret order, or original error identity changed")
	}
	got[0].Text = "changed by caller"
	again, err := diagnostic.Oracle(input, secrets...)
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("caller output mutation affected later result: %#v, %v", again, err)
	}
}

func TestOracleEmptyLeafAndDeduplication(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want []diagnostic.Message
	}{
		{"empty multi unwrap", emptyMulti{}, []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: message"}}},
		{"empty nonnil multi unwrap", emptyMulti{children: []error{}}, []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: message"}}},
		{"empty single unwrap", emptySingle{}, []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: message"}}},
		{"invalid UTF8", errors.New("ORA-01219: bad\xff"), nil},
		{"wrapper prose excluded", fmt.Errorf("ORA-99999: wrapper: %w", errors.New("no server message")), nil},
		{"deduplicated after redaction", errors.Join(errors.New("ORA-01017: password=one"), errors.New("ORA-01017: password=two")), []diagnostic.Message{{Code: "ORA-01017", Text: "ORA-01017: password=[REDACTED]"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.Oracle(tc.err)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Fatalf("Oracle() = %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestOracleRedactionBeforeStackSplitting(t *testing.T) {
	encodedSecret := "my/private pass+%"
	for _, tc := range []struct {
		name, input string
		secrets     []string
		want        string
	}{
		{"mixed case", "PaSsWoRd : 'two words'; PWD=x; token=\"other words\"", nil, "PaSsWoRd=[REDACTED]; PWD=[REDACTED]; token=[REDACTED]"},
		{"unterminated quote", "password='two words", nil, "password=[REDACTED]"},
		{"URL contains Oracle marker", "oracle://reader:myORA-01031:secret@host/db", nil, "[REDACTED CONNECTION]"},
		{"assignment contains Oracle marker", "password='myORA-01031:secret'", nil, "password=[REDACTED]"},
		{"secret crosses Oracle marker", "sent myORA-01031:secret", []string{"myORA-01031:secret"}, "sent [REDACTED]"},
		{"secret overlaps marker start", "sent myORA-01031:secret", []string{"myORA"}, "sent [REDACTED]-01031:secret"},
		{"code token retained", "database not open", []string{"ORA", "01219"}, "database not open"},
		{"overlapping secrets", "value=abcdef abc", []string{"abc", "abcdef"}, "value=[REDACTED] [REDACTED]"},
		{"encoded secret", "value=" + url.QueryEscape(encodedSecret) + " " + url.PathEscape(encodedSecret), []string{encodedSecret}, "value=[REDACTED] [REDACTED]"},
		{"control bytes", "one\x00two\x1bthree\r\nfour\rfive\tsix", nil, "one two three\nfour\nfive\tsix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.Oracle(errors.New("ORA-01219: "+tc.input), tc.secrets...)
			want := []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: " + tc.want}}
			if err != nil || !slices.Equal(got, want) {
				t.Fatalf("Oracle() = %#v, %v; want %#v", got, err, want)
			}
			again, err := diagnostic.Oracle(errors.New(got[0].Text))
			if err != nil || !slices.Equal(again, want) {
				t.Fatalf("redaction without new secrets changed output: %#v, %v", again, err)
			}
		})
	}
}

func wrap(err error, count int) error {
	for range count {
		err = fmt.Errorf("wrapped: %w", err)
	}
	return err
}

func TestOracleCapacityBoundariesAndNoPrefix(t *testing.T) {
	exact := "ORA-01219: " + strings.Repeat("x", diagnostic.MaxBytes-len("ORA-01219: "))
	stack := func(n int) string {
		var out strings.Builder
		for i := range n {
			fmt.Fprintf(&out, "ORA-01031: failure %d\n", i)
		}
		return out.String()
	}
	manyChildren := make([]error, 65)
	for i := range manyChildren {
		manyChildren[i] = errors.New("ORA-01031: same message")
	}
	secretBytes := strings.Repeat("z", diagnostic.MaxBytes)
	for _, tc := range []struct {
		name      string
		err       error
		secrets   []string
		wantCount int
		capacity  bool
	}{
		{"exact leaf bytes", errors.New(exact), nil, 1, false},
		{"over leaf bytes", errors.New(exact + "x"), nil, 0, true},
		{"aggregate output bytes", errors.Join(errors.New(exact), errors.New("ORA-01031: next")), nil, 0, true},
		{"redaction expansion", errors.New("ORA-01219: " + strings.Repeat("x", diagnostic.MaxBytes/2)), []string{"x"}, 0, true},
		{"exact message count", errors.New(stack(32)), nil, 32, false},
		{"over message count after valid prefix", errors.New(stack(33)), nil, 0, true},
		{"duplicates do not exhaust messages", errors.New(strings.Repeat("ORA-01031: same message\n", 33)), nil, 1, false},
		{"exact graph nodes", wrap(errors.New("ORA-01219: message"), 63), nil, 1, false},
		{"over graph nodes", wrap(errors.New("ORA-01219: message"), 64), nil, 0, true},
		{"cycle", &cycle{}, nil, 0, true},
		{"wide graph", errors.Join(manyChildren...), nil, 0, true},
		{"exact secret count", errors.New("ORA-01219: message"), make([]string, 64), 1, false},
		{"over secret count", errors.New("ORA-01219: message"), make([]string, 65), 0, true},
		{"exact secret bytes", errors.New("ORA-01219: message"), []string{secretBytes}, 1, false},
		{"over secret bytes", errors.New("ORA-01219: message"), []string{secretBytes, "x"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.Oracle(tc.err, tc.secrets...)
			if tc.capacity {
				if !errors.Is(err, diagnostic.ErrCapacity) || got != nil {
					t.Fatalf("Oracle() = %#v, %v; want nil, ErrCapacity", got, err)
				}
			} else if err != nil || len(got) != tc.wantCount {
				t.Fatalf("Oracle() returned %d messages, %v; want %d, nil", len(got), err, tc.wantCount)
			}
		})
	}
}

func TestOracleConcurrentCallsOwnResults(t *testing.T) {
	secrets := []string{"private"}
	raw := errors.New("ORA-01219: value=private")
	want := []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: value=[REDACTED]"}}
	var calls sync.WaitGroup
	for range 16 {
		calls.Go(func() {
			for range 10 {
				got, err := diagnostic.Oracle(raw, secrets...)
				if err != nil || !slices.Equal(got, want) {
					t.Errorf("Oracle() = %#v, %v; want %#v", got, err, want)
					return
				}
				got[0].Text = "caller mutation"
			}
		})
	}
	calls.Wait()
	if raw.Error() != "ORA-01219: value=private" || !slices.Equal(secrets, []string{"private"}) {
		t.Fatal("concurrent calls mutated input")
	}
}

func TestOraclePreservedCodeDoesNotHideOverlappingSecret(t *testing.T) {
	got, err := diagnostic.Oracle(errors.New("ORA-01219: value=ORA-01031:secret"), "ORA", "A-01031:secret")
	want := []diagnostic.Message{{Code: "ORA-01219", Text: "ORA-01219: value=OR[REDACTED]"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Oracle() = %#v, %v; want %#v", got, err, want)
	}
}
