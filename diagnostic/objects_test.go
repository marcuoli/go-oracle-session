package diagnostic_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/marcuoli/go-oracle-session/diagnostic"
)

func TestStatementObjectsAttributesOnlyAnExactReportedName(t *testing.T) {
	objects := []diagnostic.ObjectRef{
		{Schema: "App", Name: "Live.Rows", Kind: "table"},
		{Schema: "REPORT", Name: "LIVE", Kind: "view"},
		{Schema: "APP", Name: "LIVE", Kind: "table"},
	}
	missing := errors.New("ORA-00942: table or view \"App\".\"Live.Rows\" does not exist error occur at position: 24")
	cases := []struct {
		name string
		err  error
		mark int
	}{
		{"successful", nil, -1},
		{"named", fmt.Errorf("query failed: %w", missing), 0},
		{"same-name-other-schema", errors.New("ORA-00942: table or view REPORT.LIVE does not exist"), 1},
		{"unquoted-identity", errors.New("ORA-00942: table or view APP.LIVE does not exist"), 2},
		{"unnamed-with-offset", errors.New("ORA-00942: table or view does not exist error occur at position: 24"), -1},
		{"insufficient-privileges", errors.New("ORA-01031: insufficient privileges"), -1},
		{"unlisted-synonym-target", errors.New("ORA-00942: table or view APP.BASE_TABLE does not exist"), -1},
		{"case-sensitive", errors.New("ORA-00942: table or view \"APP\".\"Live.Rows\" does not exist"), -1},
		{"unqualified", errors.New("ORA-00942: table or view LIVE does not exist"), -1},
		{"remote", errors.New("ORA-00942: table or view APP.LIVE@WEST does not exist"), -1},
		{"conflicting", errors.Join(missing, errors.New("ORA-00942: table or view APP.LIVE does not exist")), -1},
		{"unnamed-plus-named", errors.Join(missing, errors.New("ORA-00942: table or view does not exist")), -1},
		{"repeated-evidence", errors.Join(missing, missing), 0},
		{"nested-text-without-structured-boundary", errors.New("ORA-06550: line 1, column 24:\r\nORA-00942: table or view APP.LIVE does not exist\r\nORA-06512: at line 1"), -1},
		{"application-defined-multiline-message", errors.New("ORA-20000: application detail:\nORA-00942: table or view APP.LIVE does not exist"), -1},
		{"lookalike-in-prose", errors.New("source text contained ORA-00942: table or view APP.LIVE does not exist"), -1},
		{"wrapper-prose", objectProse{cause: errors.New("connection closed")}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expected := make([]diagnostic.ObjectStatus, len(objects))
			for i, object := range objects {
				expected[i].ObjectRef = object
				if i == tc.mark {
					expected[i].ProblemCode = "ORA-00942"
				}
			}
			got, err := diagnostic.StatementObjects(tc.err, objects)
			if err != nil || !reflect.DeepEqual(got, expected) {
				t.Fatalf("report = %+v, error = %v, want %+v", got, err, expected)
			}
			got[0].Name = "replaced"
			if objects[0].Name != "Live.Rows" {
				t.Fatal("report shares mutable storage with the catalog list")
			}
		})
	}
}

type objectProse struct{ cause error }

func (e objectProse) Error() string { return "ORA-00942: table or view APP.LIVE does not exist" }
func (e objectProse) Unwrap() error { return e.cause }

func TestStatementObjectsKeepsTheListWhenAttributionIsUnavailable(t *testing.T) {
	objects := []diagnostic.ObjectRef{{Schema: "APP", Name: "LIVE", Kind: "table"}}
	named := errors.New("ORA-00942: table or view APP.LIVE does not exist")
	got, err := diagnostic.StatementObjects(errors.Join(named, errors.New(strings.Repeat("x", diagnostic.MaxBytes+1))), objects)
	want := []diagnostic.ObjectStatus{{ObjectRef: objects[0]}}
	if !errors.Is(err, diagnostic.ErrCapacity) || !reflect.DeepEqual(got, want) {
		t.Fatalf("over-capacity diagnostic returned %+v, %v, want full catalog and no mark", got, err)
	}
	got, err = diagnostic.StatementObjects(errors.New("ORA-00942: table or view does not exist"), objects)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("generic error marked a sole reference: %+v, %v", got, err)
	}
	got, err = diagnostic.StatementObjects(named, append(objects, objects[0]))
	if err != nil || len(got) != 2 || got[0].ProblemCode != "" || got[1].ProblemCode != "" {
		t.Fatalf("duplicate entries were attributed: %+v, %v", got, err)
	}
}

func TestStatementObjectsRejectsInvalidCatalogMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []diagnostic.ObjectRef
		want error
	}{
		{"name-required", []diagnostic.ObjectRef{{Schema: "APP"}}, diagnostic.ErrInvalidObject},
		{"invalid-text", []diagnostic.ObjectRef{{Name: "T\nbad"}}, diagnostic.ErrInvalidObject},
		{"bytes", []diagnostic.ObjectRef{{Name: strings.Repeat("X", diagnostic.MaxBytes+1)}}, diagnostic.ErrCapacity},
		{"object-count", make([]diagnostic.ObjectRef, diagnostic.MaxStatementObjects+1), diagnostic.ErrCapacity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := diagnostic.StatementObjects(nil, tc.in); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("invalid catalog returned %+v, %v, want %v", got, err, tc.want)
			}
		})
	}
}

func TestStatementObjectsDoesNotAttributeRemoteEvidenceToLocalObjects(t *testing.T) {
	objects := []diagnostic.ObjectRef{{Schema: "APP", Name: "LIVE", Kind: "table"}}
	reported := errors.New("ORA-00942: table or view APP.LIVE does not exist")
	for _, tc := range []struct {
		name    string
		err     error
		objects []diagnostic.ObjectRef
	}{
		{"remote-stack", errors.New(reported.Error() + "\nORA-02063: preceding line from WEST"), objects},
		{"joined-remote-stack", errors.Join(reported, errors.New("ORA-02068: following severe error from WEST")), objects},
		{"catalog-includes-remote", reported, []diagnostic.ObjectRef{objects[0], {Schema: "APP", Name: "LIVE", DatabaseLink: "WEST"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := diagnostic.StatementObjects(tc.err, tc.objects)
			if err != nil || len(got) != len(tc.objects) {
				t.Fatalf("report lost catalog entries: %+v, %v", got, err)
			}
			for i, object := range got {
				if object.ObjectRef != tc.objects[i] || object.ProblemCode != "" {
					t.Fatalf("ambiguous remote report: %+v", got)
				}
			}
		})
	}
}
