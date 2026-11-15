package oraclenet

import (
	"errors"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrNetConfigCapacity reports input larger than the shared 1 MiB file bound.
	ErrNetConfigCapacity = errors.New("Oracle Net configuration exceeds 1 MiB")
	// ErrNetConfigEncoding reports input that is not NUL-free UTF-8 text.
	ErrNetConfigEncoding = errors.New("Oracle Net configuration requires NUL-free UTF-8")
	// ErrNetConfigSyntax reports unsupported or malformed top-level syntax without input values.
	ErrNetConfigSyntax = errors.New("Oracle Net configuration syntax is invalid or unsupported")
)

// TNSName is one effective case-normalized alias and its complete descriptor.
// Descriptor retains text after comment removal; duplicate aliases use the last definition.
type TNSName struct {
	Name       string `json:"name"`
	Descriptor string `json:"descriptor"`
}

// NetInclude retains an IFILE value literally, including any surrounding quotes.
// It is a declaration only; parsing never opens, expands or resolves the value.
type NetInclude struct {
	Value string `json:"value"`
}

// TNSNamesConfig contains effective aliases sorted by name and includes in source order.
// Includes remain unresolved, so this describes only the supplied file.
type TNSNamesConfig struct {
	Entries  []TNSName    `json:"entries"`
	Includes []NetInclude `json:"includes"`
}

// NetSetting is one top-level assignment with an uppercase name and literal value.
// Nested values are opaque text; no parameter-specific meaning is inferred.
type NetSetting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SQLNetConfig retains all top-level assignments in source order, including
// duplicates and IFILE, plus the unresolved include declarations in that order.
type SQLNetConfig struct {
	Entries  []NetSetting `json:"entries"`
	Includes []NetInclude `json:"includes"`
}

// ParseTNSNames parses at most 1 MiB of NUL-free UTF-8 without filesystem,
// environment, wallet or network access. It uses the resolver's alias/descriptor
// parser, including case normalization, multiple aliases and last-definition-wins.
// Unlike resolver lookup, unsupported scalar assignments and trailing syntax fail
// explicitly. IFILE declarations remain literal and unresolved. Errors are static
// sentinels and return no prefix; callers retain original bytes separately.
func ParseTNSNames(data []byte) (TNSNamesConfig, error) {
	if err := validateNetConfig(data); err != nil {
		return TNSNamesConfig{}, err
	}
	entries, includes, err := parseTNSContent(string(data), true)
	if err != nil {
		return TNSNamesConfig{}, ErrNetConfigSyntax
	}
	result := TNSNamesConfig{Includes: includes}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		result.Entries = append(result.Entries, TNSName{Name: name, Descriptor: entries[name]})
	}
	return result, nil
}

// ParseSQLNet parses at most 1 MiB of NUL-free UTF-8 using the existing top-level
// assignment parser. Comments are removed outside double quotes; nested values,
// quoted paths and duplicate assignments retain their existing literal semantics.
// A nonempty scalar is one unquoted token without whitespace or quotes, or one complete
// double-quoted token; nested values remain opaque to scalar admission.
// IFILE values are recorded without expansion or I/O. Parsing does not validate
// parameter-specific meanings or included files. Static sentinel errors return
// no prefix, and original source bytes remain the caller's responsibility.
func ParseSQLNet(data []byte) (SQLNetConfig, error) {
	if err := validateNetConfig(data); err != nil {
		return SQLNetConfig{}, err
	}
	settings, err := netAssignments(stripTNSComments(string(data)))
	if err != nil {
		return SQLNetConfig{}, ErrNetConfigSyntax
	}
	var result SQLNetConfig
	for _, setting := range settings {
		if !completeNetScalar(setting.value) {
			return SQLNetConfig{}, ErrNetConfigSyntax
		}
		result.Entries = append(result.Entries, NetSetting{Name: setting.name, Value: setting.value})
		if setting.name == "IFILE" {
			if netScalar(setting.value) == "" {
				return SQLNetConfig{}, ErrNetConfigSyntax
			}
			result.Includes = append(result.Includes, NetInclude{Value: setting.value})
		}
	}
	return result, nil
}

// The shared scanner frames assignments and parenthesized values. Pure parsing
// additionally requires a whole scalar token: unmatched/embedded quotes or a
// second token cannot count as complete metadata. Legacy resolver and wallet
// callers retain their existing value admission through netAssignments.
func completeNetScalar(value string) bool {
	if strings.HasPrefix(value, "(") {
		return true
	}
	if strings.HasPrefix(value, `"`) {
		return len(value) >= 2 && strings.HasSuffix(value, `"`) && strings.Count(value, `"`) == 2
	}
	return !strings.ContainsRune(value, '"') && strings.IndexFunc(value, unicode.IsSpace) < 0
}
func validateNetConfig(data []byte) error {
	if len(data) > maxTNSNamesSize {
		return ErrNetConfigCapacity
	}
	if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return ErrNetConfigEncoding
	}
	return nil
}

// Both entry points share the resolver scanner. Strict admission additionally
// rejects skipped syntax so an audit summary cannot mistake ignored text for
// fully parsed input; the existing resolver keeps its permissive lookup behavior.
func parseTNSContent(content string, strict bool) (map[string]string, []NetInclude, error) {
	content = stripTNSComments(content)
	result := make(map[string]string)
	var includes []NetInclude
	for cursor := 0; cursor < len(content); {
		cursor = skipTNSWhitespace(content, cursor)
		if cursor >= len(content) {
			break
		}
		equals := strings.IndexByte(content[cursor:], '=')
		if equals < 0 {
			if strict {
				return nil, nil, ErrNetConfigSyntax
			}
			break
		}
		equals += cursor
		aliases := strings.TrimSpace(content[cursor:equals])
		cursor = skipTNSWhitespace(content, equals+1)
		if strict && strings.EqualFold(aliases, "IFILE") {
			end := strings.IndexByte(content[cursor:], '\n')
			if end < 0 {
				end = len(content)
			} else {
				end += cursor
			}
			settings, err := netAssignments("IFILE=" + content[cursor:end])
			if err != nil || len(settings) != 1 || !completeNetScalar(settings[0].value) || netScalar(settings[0].value) == "" {
				return nil, nil, ErrNetConfigSyntax
			}
			includes = append(includes, NetInclude{Value: settings[0].value})
			cursor = end
			continue
		}
		if cursor >= len(content) || content[cursor] != '(' {
			if strict {
				return nil, nil, ErrNetConfigSyntax
			}
			if next := strings.IndexByte(content[cursor:], '\n'); next >= 0 {
				cursor += next + 1
				continue
			}
			break
		}
		end, err := scanTNSDescriptor(content, cursor)
		if err != nil {
			return nil, nil, err
		}
		descriptor := strings.TrimSpace(content[cursor:end])
		for _, rawAlias := range strings.Split(aliases, ",") {
			alias := strings.TrimSpace(rawAlias)
			if !tnsAliasPattern.MatchString(alias) {
				return nil, nil, errors.New("invalid TNS alias")
			}
			result[strings.ToUpper(alias)] = descriptor
		}
		cursor = end
	}
	return result, includes, nil
}
