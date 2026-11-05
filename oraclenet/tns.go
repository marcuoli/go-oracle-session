// Package oraclenet owns Oracle Net parsing and connection planning without a database driver.
package oraclenet

import (
	"errors"
	"fmt"
	"io"

	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

const maxTNSNamesSize = 1 << 20

var tnsAliasPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// IsAlias reports whether a name uses the supported Oracle Net alias syntax.
func IsAlias(value string) bool { return tnsAliasPattern.MatchString(value) }

// IsEasyConnect distinguishes URL and host/service inputs from TNS aliases.
func IsEasyConnect(value string) bool {
	return strings.Contains(value, "://") || strings.Contains(value, "/") || strings.Contains(value, ":")
}

// ResolveAlias looks up a case-insensitive alias in a bounded regular tnsnames.ora.
// Original descriptor text is retained after comment removal. Multiple names and
// last-definition-wins semantics are intentional compatibility behavior. Unknown
// names and invalid/unavailable files return a typed ResolutionError. No dialing
// or wallet loading occurs; adminDirectory must identify the configuration root.
func ResolveAlias(alias, adminDirectory string) (string, error) {
	alias = strings.TrimSpace(alias)
	if !tnsAliasPattern.MatchString(alias) {
		return "", newTNSResolutionError(TNSInvalidAlias, "", "", nil)
	}
	data, err := readTNSNames(alias, adminDirectory)
	if err != nil {
		return "", err
	}
	path := filepath.Join(strings.TrimSpace(adminDirectory), "tnsnames.ora")
	descriptors, err := parseTNSNames(string(data))
	if err != nil {
		return "", newTNSResolutionError(TNSFileMalformed, alias, path, err)
	}
	if descriptor := descriptors[strings.ToUpper(alias)]; descriptor != "" {
		return descriptor, nil
	}
	return "", newTNSResolutionError(TNSAliasNotFound, alias, path, nil)
}

// ValidateTNSFiles checks the shared directory and readable, bounded regular
// file before connection work. Alias contents remain a per-target check.
func ValidateTNSFiles(alias, adminDirectory string) error {
	_, err := readTNSNames(alias, adminDirectory)
	return err
}

func readTNSNames(alias, adminDirectory string) ([]byte, error) {
	directory := strings.TrimSpace(adminDirectory)
	if directory == "" {
		return nil, newTNSResolutionError(TNSAdminNotConfigured, alias, "", nil)
	}
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, newTNSResolutionError(TNSAdminUnavailable, alias, directory, err)
	}

	path := filepath.Join(directory, "tnsnames.ora")
	info, err = os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, newTNSResolutionError(TNSFileUnavailable, alias, path, err)
	}
	if info.Size() > maxTNSNamesSize {
		return nil, newTNSResolutionError(TNSFileTooLarge, alias, path, nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, newTNSResolutionError(TNSFileUnavailable, alias, path, err)
	}
	defer file.Close()
	// Bound the actual read as well as the earlier stat: a configuration file can
	// grow between those operations without allowing an unbounded allocation.
	data, err := io.ReadAll(io.LimitReader(file, maxTNSNamesSize+1))
	if err != nil {
		return nil, newTNSResolutionError(TNSFileUnavailable, alias, path, err)
	}
	if len(data) > maxTNSNamesSize {
		return nil, newTNSResolutionError(TNSFileTooLarge, alias, path, nil)
	}
	return data, nil
}

func parseTNSNames(content string) (map[string]string, error) {
	content = stripTNSComments(content)
	result := make(map[string]string)
	for cursor := 0; cursor < len(content); {
		cursor = skipTNSWhitespace(content, cursor)
		if cursor >= len(content) {
			break
		}
		equals := strings.IndexByte(content[cursor:], '=')
		if equals < 0 {
			break
		}
		equals += cursor
		aliases := strings.TrimSpace(content[cursor:equals])
		cursor = skipTNSWhitespace(content, equals+1)
		if cursor >= len(content) || content[cursor] != '(' {
			if next := strings.IndexByte(content[cursor:], '\n'); next >= 0 {
				cursor += next + 1
				continue
			}
			break
		}

		end, err := scanTNSDescriptor(content, cursor)
		if err != nil {
			return nil, err
		}
		descriptor := strings.TrimSpace(content[cursor:end])
		for _, rawAlias := range strings.Split(aliases, ",") {
			alias := strings.TrimSpace(rawAlias)
			if !tnsAliasPattern.MatchString(alias) {
				return nil, fmt.Errorf("invalid TNS alias")
			}
			result[strings.ToUpper(alias)] = descriptor
		}
		cursor = end
	}
	return result, nil
}

func stripTNSComments(content string) string {
	var result strings.Builder
	result.Grow(len(content))
	for _, line := range strings.SplitAfter(content, "\n") {
		quoted, end := false, len(line)
		for index, r := range line {
			if r == '"' {
				quoted = !quoted
			}
			if r == '#' && !quoted {
				end = index
				break
			}
		}
		// Rune offsets are byte indexes. Appending the complete non-comment prefix
		// also preserves a final multibyte rune when the file has no trailing newline.
		result.WriteString(line[:end])
		if end < len(line) && strings.HasSuffix(line, "\n") {
			result.WriteByte('\n')
		}
	}
	return result.String()
}
func skipTNSWhitespace(content string, cursor int) int {
	for cursor < len(content) && unicode.IsSpace(rune(content[cursor])) {
		cursor++
	}
	return cursor
}

func scanTNSDescriptor(content string, start int) (int, error) {
	depth := 0
	quoted := false
	for cursor := start; cursor < len(content); cursor++ {
		switch content[cursor] {
		case '"':
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
				if depth == 0 {
					return cursor + 1, nil
				}
				if depth < 0 {
					return 0, errors.New("unbalanced TNS descriptor")
				}
			}
		}
	}
	return 0, errors.New("unterminated TNS descriptor")
}
