package diagnostic

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxStatementObjects bounds the number of reviewed references in one report.
const MaxStatementObjects = 128

// ErrInvalidObject rejects a missing object name or invalid catalog text.
var ErrInvalidObject = errors.New("invalid statement object metadata")

// ObjectRef describes a direct reference declared by the SQL catalog for the
// selected statement. Schema and Name use dictionary spelling without SQL
// quotes, including the exact case of quoted identifiers. Schema may be empty
// when unresolved. Kind is caller-supplied (for example, "table" or "view") and
// may be empty. DatabaseLink is empty for a local reference.
type ObjectRef struct {
	Schema       string `json:"schema,omitempty"`
	Name         string `json:"name"`
	Kind         string `json:"kind,omitempty"`
	DatabaseLink string `json:"database_link,omitempty"`
}

// ObjectStatus pairs a reviewed object with an optional confirmed Oracle code.
// An empty ProblemCode means the report has no exact attribution for this entry.
type ObjectStatus struct {
	ObjectRef
	ProblemCode string `json:"problem_code,omitempty"`
}

const missingObjectCode = "ORA-00942"
const oracleIdentifier = `(?:"[^"\r\n]+"|[A-Z][A-Z0-9_$#]*)`

var missingObject = regexp.MustCompile(`^ORA-00942:[ \t]*table or view[ \t]+(` +
	oracleIdentifier + `)[ \t]*\.[ \t]*(` + oracleIdentifier +
	`)[ \t]+does not exist(?: error occur at position: [0-9]+)?$`)

var remoteObjectError = regexp.MustCompile(`^ORA-0?206[38](?:[: \t]|$)`)

// StatementObjects copies the catalog references in order. Pass nil for err
// after successful execution. A supported ORA-00942 message naming a unique
// schema-qualified object in the primary line of an error leaf marks its exact
// local match. Bare, conflicting, remote, or unsupported evidence leaves the
// entries unmarked. Lists containing database links are also unattributed.
// ORA-00942 does not distinguish absence from inaccessible objects.
//
// The catalog slice must cover the selected statement variant. This function
// neither parses SQL nor resolves aliases, synonyms, links, or dependencies.
// It changes no execution error and returns no formatted error text.
//
// Invalid catalog input returns no report. When error evidence exceeds the
// traversal or text bound, the complete reviewed list is returned unmarked
// with ErrCapacity. The catalog is limited to MaxStatementObjects entries and
// MaxBytes of combined metadata.
func StatementObjects(err error, objects []ObjectRef) ([]ObjectStatus, error) {
	if len(objects) > MaxStatementObjects {
		return nil, ErrCapacity
	}
	if len(objects) == 0 {
		return nil, nil
	}
	remaining := MaxBytes
	allLocal := true
	report := make([]ObjectStatus, len(objects))
	for i, object := range objects {
		if object.Name == "" {
			return nil, ErrInvalidObject
		}
		for _, value := range [...]string{object.Schema, object.Name, object.Kind, object.DatabaseLink} {
			if len(value) > remaining {
				return nil, ErrCapacity
			}
			remaining -= len(value)
			if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
				return nil, ErrInvalidObject
			}
		}
		report[i].ObjectRef = object
		allLocal = allLocal && object.DatabaseLink == ""
	}
	if err == nil || !allLocal {
		return report, nil
	}

	// Compare private leaf evidence against reviewed identities. Reformatting
	// or redacting it first could manufacture a different quoted name.
	var schema, name string
	ambiguous := false
	failure := walkLeaves(err, false, func(leaf error, _ ConnectionKind) error {
		message := leaf.Error()
		if len(message) > MaxBytes {
			return ErrCapacity
		}
		if !utf8.ValidString(message) {
			ambiguous = true
			return nil
		}
		// Only the primary line can nominate an object. Application-defined
		// messages and nested stacks may contain arbitrary continuation text.
		first, rest, _ := strings.Cut(strings.TrimSpace(message), "\n")
		first = strings.TrimSpace(first)
		if remoteObjectError.MatchString(first) {
			ambiguous = true
			return nil
		}
		for line := range strings.SplitSeq(rest, "\n") {
			line = strings.TrimSpace(line)
			if remoteObjectError.MatchString(line) || strings.HasPrefix(line, missingObjectCode+":") {
				ambiguous = true
				return nil
			}
		}
		if !strings.HasPrefix(first, missingObjectCode+":") {
			return nil
		}
		match := missingObject.FindStringSubmatch(first)
		if match == nil {
			ambiguous = true
			return nil
		}
		owner, object := strings.Trim(match[1], `"`), strings.Trim(match[2], `"`)
		if name != "" && (schema != owner || name != object) {
			ambiguous = true
		}
		schema, name = owner, object
		return nil
	})
	if failure != nil || ambiguous || name == "" {
		return report, failure
	}
	matched := -1
	for i, object := range report {
		if object.Schema == schema && object.Name == name {
			if matched != -1 {
				return report, nil
			}
			matched = i
		}
	}
	if matched != -1 {
		report[matched].ProblemCode = missingObjectCode
	}
	return report, nil
}
