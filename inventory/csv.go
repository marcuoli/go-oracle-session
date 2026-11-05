// Package inventory admits bounded credential-bearing CSV without logging payloads.
package inventory

import (
	"bytes"
	"encoding/csv"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/text/cases"
	"net/url"

	textoutput "github.com/marcuoli/go-textoutput/display"
)

const (
	maxFileSize = 10 << 20
	maxRows     = 10_000
)

var (
	requiredHeaders  = []string{"name", "connect", "username", "password"}
	credentialPrefix = regexp.MustCompile(`^[^/@:\s]+/[^@\s]+@`)
)

// Entry preserves credential bytes for caller-owned connection setup; never serialize it into reports.
type Entry struct {
	Environment         string
	TargetOracleVersion string
	Name                string
	Connect             string
	Username            string
	Password            string
}

// ReadFile admits a bounded regular CSV file without logging its payload.
func ReadFile(path string, options ...Options) ([]Entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, &ValidationError{Code: FileUnreadable}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, &ValidationError{Code: FileUnsafe}
	}
	if info.Size() > maxFileSize {
		return nil, &ValidationError{Code: FileTooLarge, Limit: maxFileSize}
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, &ValidationError{Code: FileUnreadable}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return nil, &ValidationError{Code: FileReadFailed}
	}
	if len(data) > maxFileSize {
		return nil, &ValidationError{Code: FileTooLarge, Limit: maxFileSize}
	}
	return Parse(data, options...)
}

// Parse admits named endpoints, retaining exact passwords and unique effective labels.
func Parse(data []byte, options ...Options) ([]Entry, error) {
	if len(data) > maxFileSize {
		return nil, &ValidationError{Code: FileTooLarge, Limit: maxFileSize}
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comment = '#'
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false

	var records [][]string
	var sourceLines []int
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &ValidationError{Code: CSVInvalid}
		}
		line, _ := reader.FieldPos(0)
		records = append(records, record)
		sourceLines = append(sourceLines, line)
	}
	if len(records) == 0 {
		return nil, &ValidationError{Code: CSVEmpty}
	}
	if len(records)-1 > maxRows {
		return nil, &ValidationError{Code: TooManyRows, Limit: maxRows}
	}

	headers := records[0]
	if len(headers) > 0 {
		headers[0] = strings.TrimPrefix(headers[0], "\uFEFF")
	}
	indexes, err := validateHeaders(headers)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(records)-1)
	seen := make(map[string]int, len(records)-1)
	seenLabels := make(map[string]int, len(records)-1)
	fold := cases.Fold()
	for recordIndex, record := range records[1:] {
		row := sourceLines[recordIndex+1]
		if len(record) != len(headers) {
			return nil, &ValidationError{Code: RowFieldCount, Row: row, Actual: len(record), Expected: len(headers)}
		}

		entry := Entry{
			Name:     strings.TrimSpace(record[indexes["name"]]),
			Connect:  strings.TrimSpace(record[indexes["connect"]]),
			Username: strings.TrimSpace(record[indexes["username"]]),
			Password: record[indexes["password"]],
		}
		if index, present := indexes["environment"]; present {
			entry.Environment = strings.TrimSpace(record[index])
		}
		if index, present := indexes["target_oracle_version"]; present {
			entry.TargetOracleVersion = strings.TrimSpace(record[index])
		}
		label := entry.Name
		effectiveLabel := textoutput.TargetLabel(entry.Name, "")

		if entry.Name == "" || effectiveLabel == "" {
			return nil, &ValidationError{Code: RowEmptyField, Row: row, Field: "name"}
		}
		if entry.Connect == "" {
			return nil, &ValidationError{Code: RowEmptyField, Row: row, Target: textoutput.TargetLabel(label), Field: "connect"}
		}
		if entry.Username == "" {
			return nil, &ValidationError{Code: RowEmptyField, Row: row, Target: textoutput.TargetLabel(label), Field: "username"}
		}
		if entry.Password == "" {
			return nil, &ValidationError{Code: RowEmptyField, Row: row, Target: textoutput.TargetLabel(label), Field: "password"}
		}
		if validationErr := validateLengths(entry); validationErr != nil {
			validationErr.Row = row
			validationErr.Target = textoutput.TargetLabel(label)
			return nil, validationErr
		}
		if hasEmbeddedCredentials(entry.Connect, options...) {
			return nil, &ValidationError{Code: EmbeddedCredentials, Row: row, Target: textoutput.TargetLabel(label)}
		}
		nameKey := fold.String(entry.Name)
		if firstRow, exists := seen[nameKey]; exists {
			return nil, &ValidationError{Code: DuplicateName, Row: row, FirstRow: firstRow, Target: textoutput.TargetLabel(label)}
		}
		// Collection uses the terminal-safe, truncated label as parent identity.
		// Raw names must remain intact, but their effective labels must be unique
		// too so controls, ANSI styling, or long suffixes cannot merge targets.
		labelKey := fold.String(effectiveLabel)
		if firstRow, exists := seenLabels[labelKey]; exists {
			return nil, &ValidationError{Code: DuplicateName, Row: row, FirstRow: firstRow, Target: effectiveLabel}
		}
		seen[nameKey] = row
		seenLabels[labelKey] = row
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil, &ValidationError{Code: NoDatabaseRows}
	}
	return entries, nil
}

func validateHeaders(headers []string) (map[string]int, error) {
	// Validate names rather than a fixed count: environment is optional, while
	// unknown, duplicate and missing required headers remain errors.
	wanted := make(map[string]struct{}, len(requiredHeaders))
	wanted["environment"] = struct{}{}
	wanted["target_oracle_version"] = struct{}{}
	for _, header := range requiredHeaders {
		wanted[header] = struct{}{}
	}

	indexes := make(map[string]int, len(headers))
	for index, raw := range headers {
		header := strings.TrimSpace(raw)
		if _, ok := wanted[header]; !ok {
			return nil, &ValidationError{Code: HeaderUnknown, Index: index + 1}
		}
		if _, duplicate := indexes[header]; duplicate {
			return nil, &ValidationError{Code: HeaderDuplicated, Header: header}
		}
		indexes[header] = index
	}
	for _, header := range requiredHeaders {
		if _, ok := indexes[header]; !ok {
			return nil, &ValidationError{Code: HeaderMissing, Header: header}
		}
	}
	return indexes, nil
}

func validateLengths(entry Entry) *ValidationError {
	switch {
	case len(entry.TargetOracleVersion) > 100:
		return &ValidationError{Code: RowFieldTooLong, Field: "target_oracle_version", Limit: 100}
	case len(entry.Name) > 200:
		return &ValidationError{Code: RowFieldTooLong, Field: "name", Limit: 200}
	case len(entry.Connect) > 4096:
		return &ValidationError{Code: RowFieldTooLong, Field: "connect", Limit: 4096}
	case len(entry.Username) > 256:
		return &ValidationError{Code: RowFieldTooLong, Field: "username", Limit: 256}
	case len(entry.Password) > 32768:
		return &ValidationError{Code: RowFieldTooLong, Field: "password", Limit: 32768}
	default:
		return nil
	}
}

// Options delegates engine-specific URL policy while keeping CSV mechanics shared.
type Options struct{ ValidateURL func(string) error }

func hasEmbeddedCredentials(connect string, options ...Options) bool {
	if credentialPrefix.MatchString(connect) {
		return true
	}
	if !strings.Contains(connect, "://") {
		return false
	}
	if len(options) > 0 && options[0].ValidateURL != nil {
		return options[0].ValidateURL(connect) != nil
	}
	parsed, err := url.Parse(connect)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return true
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(strings.ReplaceAll(key, "_", " "))
		if strings.Contains(normalized, "password") || normalized == "user" || normalized == "username" || normalized == "uid" || normalized == "pwd" || normalized == "token" {
			return true
		}
	}
	return false
}
