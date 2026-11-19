package inventory

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// WalletReference identifies an external wallet credential without loading it.
// Directory is an absolute path for the current platform. Credential is an
// exact, case-sensitive key. Admission does not expand either value, check file
// existence, read wallet contents or resolve database credentials.
type WalletReference struct {
	Directory  string
	Credential string
}

// AuthEntry extends an admitted Entry with explicit authentication selection.
// Auth is "password" or "wallet"; Wallet is non-nil only for wallet rows, whose
// Username and Password are empty. Passwords and references must not be logged
// or serialized into reports. Each admitted Wallet is owned by the caller.
type AuthEntry struct {
	Entry
	Auth   string
	Wallet *WalletReference
}

// ParseAuthenticated admits password and wallet-reference CSV rows. It accepts
// auth, wallet_directory and wallet_credential in addition to Parse's headers;
// all four legacy required headers must remain present. Empty auth selects
// password. Auth is trimmed and lowercased; references retain their exact bytes.
//
// Wallet rows forbid any nonempty raw username/password. Password rows retain
// legacy credential validation and forbid nonempty wallet fields. References
// must be nonblank valid UTF-8 without control characters, at most 4096 bytes,
// and use a current-platform absolute directory. No wallet access or environment
// expansion occurs. Other bounds, URL policy and safe errors match Parse.
// Admission returns no rows on any error.
func ParseAuthenticated(data []byte, options ...Options) ([]AuthEntry, error) {
	return parse(data, true, options...)
}

// ReadFileAuthenticated reads a bounded regular CSV file and applies
// ParseAuthenticated. File admission and redacted errors match ReadFile; wallet
// references are not opened or resolved.
func ReadFileAuthenticated(path string, options ...Options) ([]AuthEntry, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return ParseAuthenticated(data, options...)
}

func admitAuthentication(entry Entry, row []string, indexes map[string]int, enabled bool) (AuthEntry, *ValidationError) {
	result := AuthEntry{Entry: entry, Auth: "password"}
	value := func(name string) string {
		if index, present := indexes[name]; present {
			return row[index]
		}
		return ""
	}
	if enabled {
		if mode := strings.ToLower(strings.TrimSpace(value("auth"))); mode != "" {
			result.Auth = mode
		}
	}
	switch result.Auth {
	case "password":
		if entry.Username == "" {
			return AuthEntry{}, &ValidationError{Code: RowEmptyField, Field: "username"}
		}
		if entry.Password == "" {
			return AuthEntry{}, &ValidationError{Code: RowEmptyField, Field: "password"}
		}
		if enabled {
			for _, field := range []string{"wallet_directory", "wallet_credential"} {
				if value(field) != "" {
					return AuthEntry{}, &ValidationError{Code: RowFieldConflict, Field: field}
				}
			}
		}
	case "wallet":
		// Inspect the raw username: trimming must not silently discard a
		// supplied credential when wallet authentication is selected.
		for _, field := range []string{"username", "password"} {
			if value(field) != "" {
				return AuthEntry{}, &ValidationError{Code: RowFieldConflict, Field: field}
			}
		}
		directory, credential := value("wallet_directory"), value("wallet_credential")
		for _, ref := range []struct{ field, value string }{
			{"wallet_directory", directory}, {"wallet_credential", credential},
		} {
			if err := validateReference(ref.field, ref.value); err != nil {
				return AuthEntry{}, err
			}
		}
		if !filepath.IsAbs(directory) {
			return AuthEntry{}, &ValidationError{Code: RowFieldInvalid, Field: "wallet_directory"}
		}
		result.Wallet = &WalletReference{Directory: directory, Credential: credential}
	default:
		return AuthEntry{}, &ValidationError{Code: RowFieldInvalid, Field: "auth"}
	}
	return result, nil
}

func validateReference(field, value string) *ValidationError {
	if len(value) > 4096 {
		return &ValidationError{Code: RowFieldTooLong, Field: field, Limit: 4096}
	}
	if strings.TrimSpace(value) == "" {
		return &ValidationError{Code: RowEmptyField, Field: field}
	}
	if !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return &ValidationError{Code: RowFieldInvalid, Field: field}
	}
	return nil
}
