package inventory

import "fmt"

// ValidationCode is a stable diagnostic category independent of payload values.
type ValidationCode string

// FileUnreadable and the related codes describe bounded inventory failures.
const (
	FileUnreadable      ValidationCode = "file_unreadable"
	FileUnsafe          ValidationCode = "file_unsafe"
	FileTooLarge        ValidationCode = "file_too_large"
	FileReadFailed      ValidationCode = "file_read_failed"
	CSVInvalid          ValidationCode = "csv_invalid"
	CSVEmpty            ValidationCode = "csv_empty"
	TooManyRows         ValidationCode = "too_many_rows"
	RowFieldCount       ValidationCode = "row_field_count"
	RowEmptyField       ValidationCode = "row_empty_field"
	RowFieldTooLong     ValidationCode = "row_field_too_long"
	EmbeddedCredentials ValidationCode = "embedded_credentials"
	DuplicateName       ValidationCode = "duplicate_name"
	NoDatabaseRows      ValidationCode = "no_database_rows"
	HeaderFieldCount    ValidationCode = "header_field_count"
	HeaderUnknown       ValidationCode = "header_unknown"
	HeaderDuplicated    ValidationCode = "header_duplicated"
	HeaderMissing       ValidationCode = "header_missing"
)

// ValidationError exposes safe source-line and field facts, never credential bytes.
type ValidationError struct {
	Code     ValidationCode
	Row      int
	FirstRow int
	Actual   int
	Expected int
	Limit    int
	Target   string
	Field    string
	Header   string
	Index    int
}

// Error formats a redacted operator diagnostic from safe inventory facts.
func (e *ValidationError) Error() string {
	switch e.Code {
	case FileUnreadable:
		return "inventory file is not readable"
	case FileUnsafe:
		return "inventory must be a regular file and not a symbolic link"
	case FileTooLarge:
		return fmt.Sprintf("inventory exceeds the %d-byte size limit", e.Limit)
	case FileReadFailed:
		return "inventory file could not be read"
	case CSVInvalid:
		return "inventory is not valid RFC 4180 CSV"
	case CSVEmpty:
		return "inventory is empty"
	case TooManyRows:
		return fmt.Sprintf("inventory exceeds the %d-row limit", e.Limit)
	case RowFieldCount:
		return fmt.Sprintf("inventory row %d has %d fields; expected %d", e.Row, e.Actual, e.Expected)
	case RowEmptyField:
		if e.Target == "" {
			return fmt.Sprintf("inventory row %d has an empty %s", e.Row, e.Field)
		}
		if e.Field == "connect" {
			return fmt.Sprintf("inventory row %d (%s) has an empty connect value", e.Row, e.Target)
		}
		return fmt.Sprintf("inventory row %d (%s) has an empty %s", e.Row, e.Target, e.Field)
	case RowFieldTooLong:
		return fmt.Sprintf("inventory row %d (%s): %s exceeds %d bytes", e.Row, e.Target, e.Field, e.Limit)
	case EmbeddedCredentials:
		return fmt.Sprintf("inventory row %d (%s) embeds credentials in connect", e.Row, e.Target)
	case DuplicateName:
		return fmt.Sprintf("inventory line %d duplicates name %s from line %d", e.Row, e.Target, e.FirstRow)
	case NoDatabaseRows:
		return "inventory contains no database rows"
	case HeaderFieldCount:
		return fmt.Sprintf("inventory header has %d fields; expected %d", e.Actual, e.Expected)
	case HeaderUnknown:
		return fmt.Sprintf("inventory header %d is unknown", e.Index)
	case HeaderDuplicated:
		return fmt.Sprintf("inventory header %q is duplicated", e.Header)
	case HeaderMissing:
		return fmt.Sprintf("inventory header %q is missing", e.Header)
	default:
		return "inventory is invalid"
	}
}
