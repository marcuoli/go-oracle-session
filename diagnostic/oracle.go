// Package diagnostic extracts explicit, credential-redacted Oracle diagnostics.
// Ordinary session errors keep their existing safe formatting.
package diagnostic

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrCapacity means the complete error graph, secret set or message exceeded a
// diagnostic limit. No partial diagnostic is returned as a complete stack.
var ErrCapacity = errors.New("oracle diagnostic capacity exceeded")

// MaxBytes bounds text accepted from one error, all supplied secrets combined,
// and the complete returned diagnostic text.
const MaxBytes = 16 << 10

// Message retains an Oracle code and its complete redacted diagnostic text.
// Text includes Code; other database identifiers and data may remain literal.
type Message struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

const (
	maxNodes    = 64
	maxMessages = 32
	maxSecrets  = 64
)

var oracleLine = regexp.MustCompile(`ORA-[0-9]{5}:`)
var credentials = regexp.MustCompile(`(?i)\b(password|passwd|pwd|token|secret)\s*[:=]\s*(\[REDACTED\]|"[^"]*(?:"|$)|'[^']*(?:'|$)|[^\s;,\)\]]+)`)
var authorization = regexp.MustCompile(`(?i)\b(authorization)\s*[:=]\s*[^\r\n;]*`)
var connectionURL = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>]+`)

// Oracle returns owned Oracle diagnostics from leaf causes in wrapped/joined
// errors, in traversal and stack order, with duplicate redacted messages removed.
// It does not expose wrapper prose, execute SQL, mutate inputs or change
// errors.Is/As. Only ORA-nnnnn: messages with text are admitted; code-only errors
// return no messages. Non-UTF-8 leaf text is ignored.
//
// Credential assignments, authorization values and connection URLs are redacted
// before known secrets. Include connection secrets supplied by the caller; other
// database identifiers or data may remain literal. Oracle code tokens themselves
// are preserved when a known secret is contained entirely within a code token.
//
// Graphs beyond 64 nodes, more than 32 messages, more than 64 supplied secrets,
// or input/output beyond MaxBytes return ErrCapacity with no prefix. The
// function is safe for concurrent calls when the caller does not mutate inputs.
func Oracle(err error, secrets ...string) ([]Message, error) {
	secretPattern, failure := compileSecrets(secrets)
	if failure != nil {
		return nil, failure
	}
	var output []Message
	seen := map[string]bool{}
	total := 0
	failure = walkLeaves(err, false, func(leaf error, _ ConnectionKind) error {
		text := leaf.Error()
		if len(text) > MaxBytes {
			return ErrCapacity
		}
		if !utf8.ValidString(text) {
			return nil
		}
		// Redact the complete leaf before splitting: secrets can contain code markers.
		text = redact(text, secretPattern)
		starts := oracleLine.FindAllStringIndex(text, -1)
		for i, at := range starts {
			end := len(text)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			message := strings.TrimSpace(text[at[0]:end])
			if strings.TrimSpace(message[10:]) == "" || seen[message] {
				continue
			}
			total += len(message)
			if total > MaxBytes || len(output) >= maxMessages {
				return ErrCapacity
			}
			seen[message] = true
			output = append(output, Message{Code: message[:9], Text: message})
		}
		return nil
	})
	if failure != nil {
		return nil, failure
	}
	return output, nil
}

func validateSecrets(secrets []string) error {
	if len(secrets) > maxSecrets {
		return ErrCapacity
	}
	total := 0
	for _, secret := range secrets {
		total += len(secret)
		if total > MaxBytes {
			return ErrCapacity
		}
	}
	return nil
}

func compileSecrets(secrets []string) (*regexp.Regexp, error) {
	if err := validateSecrets(secrets); err != nil {
		return nil, err
	}
	values := make([]string, 0, len(secrets)*3)
	for _, secret := range secrets {
		if secret != "" {
			values = append(values, secret, url.QueryEscape(secret), url.PathEscape(secret))
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	// Copy before sorting: a caller's secret slice remains untouched. Longer
	// alternatives win when credentials share a prefix.
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	values = slices.Compact(values)
	for i, value := range values {
		values[i] = regexp.QuoteMeta(value)
	}
	pattern, err := regexp.Compile(strings.Join(values, "|"))
	if err != nil {
		return nil, ErrCapacity
	}
	return pattern, nil
}

func redact(text string, secrets *regexp.Regexp) string {
	return redactWithCodes(text, secrets, oracleLine)
}

func redactWithCodes(text string, secrets *regexp.Regexp, codeTokens *regexp.Regexp) string {
	// Structural recognition precedes literal replacement so a known secret
	// cannot hide a URL scheme or credential key from its redactor.
	text = connectionURL.ReplaceAllString(text, "[REDACTED CONNECTION]")
	text = credentials.ReplaceAllString(text, "$1=[REDACTED]")
	text = authorization.ReplaceAllString(text, "$1=[REDACTED]")
	if secrets != nil {
		codes := codeTokens.FindAllStringIndex(text, -1)
		var out strings.Builder
		last, code := 0, 0
		for search := 0; search < len(text); {
			match := secrets.FindStringIndex(text[search:])
			if match == nil {
				break
			}
			match[0] += search
			match[1] += search
			search = match[1]
			for code < len(codes) && codes[code][1] <= match[0] {
				code++
			}
			if code < len(codes) && match[0] >= codes[code][0] && match[1] <= codes[code][1] {
				// A preserved code fragment must not hide another secret
				// starting inside it and extending into the message.
				search = match[0] + 1
				continue
			}
			out.WriteString(text[last:match[0]])
			out.WriteString("[REDACTED]")
			last = match[1]
		}
		if last > 0 {
			out.WriteString(text[last:])
			text = out.String()
		}
	}
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, text)
}
