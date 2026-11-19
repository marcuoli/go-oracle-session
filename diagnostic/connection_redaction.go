package diagnostic

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Credential references without a URI scheme occur in native connection errors.
// Known-secret matching still covers quoted/space-containing password values.
var credentialReference = regexp.MustCompile(`(?:[A-Za-z0-9_.$#-]+|"[^"\r\n]+")[:/][^\s@]+@[^\s<>;,]+`)

func redactConnection(text string, secrets *regexp.Regexp) string {
	// Strip complete URLs before bare-reference matching to avoid partial URI edits.
	text = connectionURL.ReplaceAllString(text, "[REDACTED CONNECTION]")
	text = redactReferences(text)
	text = redactWithCodes(text, secrets, connectionToken)
	// A supplied password can contain spaces; after literal redaction its complete
	// authority/connect reference becomes recognizable without guessing its bytes.
	return redactReferences(text)
}
func redactReferences(text string) string {
	return credentialReference.ReplaceAllStringFunc(text, func(reference string) string {
		// Preserve a native diagnostic prefix that happens to touch a credential ref.
		if at := connectionToken.FindStringIndex(reference); at != nil && at[0] == 0 && at[1] == 9 && len(reference) > 9 && reference[9] == ':' {
			return reference[:10] + credentialReference.ReplaceAllString(reference[10:], "[REDACTED CONNECTION]")
		}
		return "[REDACTED CONNECTION]"
	})
}

func compileConnectionSecrets(secrets []string) (*regexp.Regexp, error) {
	if err := validateSecrets(secrets); err != nil {
		return nil, err
	}
	type value struct {
		text    string
		encoded bool
	}
	var values []value
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		query := url.QueryEscape(secret)
		values = append(values, value{secret, false}, value{query, true}, value{url.PathEscape(secret), true}, value{strings.ReplaceAll(query, "+", "%20"), true})
	}
	if len(values) == 0 {
		return nil, nil
	}
	// Sort by actual matched bytes, not expanded regexp length: a shorter encoded
	// alternative must not mask a prefix before a longer literal secret can match.
	slices.SortFunc(values, func(a, b value) int { return len(b.text) - len(a.text) })
	patterns := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, item := range values {
		pattern := regexp.QuoteMeta(item.text)
		if item.encoded {
			pattern = percentCasePattern(item.text)
		}
		if !seen[pattern] {
			seen[pattern] = true
			patterns = append(patterns, pattern)
		}
	}
	pattern, err := regexp.Compile(strings.Join(patterns, "|"))
	if err != nil {
		return nil, ErrCapacity
	}
	return pattern, nil
}

func percentCasePattern(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+2 < len(text) {
			out.WriteByte('%')
			for _, digit := range []byte{text[i+1], text[i+2]} {
				if digit >= 'A' && digit <= 'F' {
					out.WriteByte('[')
					out.WriteByte(digit)
					out.WriteByte(digit + ('a' - 'A'))
					out.WriteByte(']')
				} else {
					out.WriteString(regexp.QuoteMeta(string(digit)))
				}
			}
			i += 2
		} else {
			out.WriteString(regexp.QuoteMeta(string(text[i])))
		}
	}
	return out.String()
}
