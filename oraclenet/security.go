package oraclenet

import (
	"errors"
	"path/filepath"
	"strings"
)

// SecuritySetting preserves one explicit Oracle Net security setting. Name is
// canonical uppercase; Value is unquoted literal text. Duplicate settings remain
// separate so a driver adapter can reject conflicting or unsupported policies.
// Descriptor identifies a SECURITY block; otherwise the setting is from sqlnet.
type SecuritySetting struct {
	Name, Value string
	Descriptor  bool
}

// ReadSecuritySettings returns SECURITY-block settings and SSL_* sqlnet entries.
// It does not interpret or enforce transport policy, load credentials or dial.
// An empty adminDirectory performs no file discovery. sqlnet reads use the same
// regular-file/1MiB contract as wallet discovery; missing sqlnet is permitted.
// Unsupported IFILE includes return an error, never an incomplete settings view.
func ReadSecuritySettings(descriptor, adminDirectory string) ([]SecuritySetting, error) {
	if len(descriptor) > maxTNSNamesSize {
		return nil, errors.New("Oracle Net descriptor exceeds 1 MiB")
	}
	result, err := descriptorSecuritySettings(descriptor, 0)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(adminDirectory) == "" {
		return result, nil
	}
	directory, err := filepath.Abs(strings.TrimSpace(adminDirectory))
	if err != nil {
		return nil, err
	}
	text, err := readSQLNetWalletSettings(directory)
	if err != nil {
		return nil, err
	}
	settings, err := netAssignments(text)
	if err != nil {
		return nil, err
	}
	// Includes can carry certificate constraints even when a local wallet location
	// is present. Do not report an apparently complete security view without them.
	if _, found, err := uniqueNetSetting(settings, "IFILE"); err != nil {
		return nil, err
	} else if found {
		return nil, errors.New("sqlnet.ora IFILE security resolution is unsupported")
	}
	for _, setting := range settings {
		if strings.HasPrefix(setting.name, "SSL_") {
			result = append(result, SecuritySetting{Name: setting.name, Value: netScalar(setting.value)})
		}
	}
	return result, nil
}
func descriptorSecuritySettings(content string, depth int) ([]SecuritySetting, error) {
	if depth > 16 {
		return nil, errors.New("Oracle Net descriptor nesting exceeds 16 levels")
	}
	settings, err := netAssignments(content)
	if err != nil {
		return nil, err
	}
	var result []SecuritySetting
	// Includes can carry certificate constraints even when a local wallet location
	// is present. Do not report an apparently complete security view without them.
	if _, found, err := uniqueNetSetting(settings, "IFILE"); err != nil {
		return nil, err
	} else if found {
		return nil, errors.New("sqlnet.ora IFILE security resolution is unsupported")
	}
	for _, setting := range settings {
		switch setting.name {
		case "DESCRIPTION", "DESCRIPTION_LIST":
			nested, err := descriptorSecuritySettings(setting.value, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		case "SECURITY":
			security, err := netAssignments(setting.value)
			if err != nil {
				return nil, err
			}
			for _, item := range security {
				result = append(result, SecuritySetting{Name: item.name, Value: netScalar(item.value), Descriptor: true})
			}
		}
	}
	return result, nil
}
