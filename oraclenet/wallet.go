package oraclenet

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ResolveWallet resolves wallet configuration for a descriptor the driver has
// already identified as TCPS. It never reads wallet contents.
func ResolveWallet(descriptor, adminDirectory string) (string, error) {
	directory, err := filepath.Abs(strings.TrimSpace(adminDirectory))
	if err != nil {
		return "", err
	}
	wallet, found, err := ResolveDescriptorWallet(descriptor, directory)
	if err != nil {
		return "", err
	}
	if found {
		return wallet, nil
	}
	content, err := readSQLNetWalletSettings(directory)
	if err != nil {
		return "", err
	}
	settings, err := netAssignments(content)
	if err != nil {
		return "", fmt.Errorf("sqlnet.ora: %w", err)
	}
	location, found, err := uniqueNetSetting(settings, "WALLET_LOCATION")
	if err != nil {
		return "", err
	}
	if found {
		source, err := requiredNetSetting(location, "SOURCE")
		if err != nil {
			return "", err
		}
		method, err := requiredNetSetting(source, "METHOD")
		if err != nil {
			return "", err
		}
		if !strings.EqualFold(netScalar(method), "file") {
			return "", errors.New("sqlnet.ora WALLET_LOCATION requires METHOD=file")
		}
		data, err := requiredNetSetting(source, "METHOD_DATA")
		if err != nil {
			return "", err
		}
		value, err := requiredNetSetting(data, "DIRECTORY")
		if err != nil {
			return "", err
		}
		return resolveWalletDirectory(value, directory)
	}
	if _, found, err := uniqueNetSetting(settings, "IFILE"); err != nil {
		return "", err
	} else if found {
		return "", errors.New("sqlnet.ora IFILE wallet resolution is unsupported; configure WALLET_LOCATION directly")
	}
	// A downloaded ADB wallet can be used as TNS_ADMIN without sqlnet.ora.
	// Absence is valid for walletless TLS; an explicit bad location above never
	// silently falls back to a different wallet.
	info, err := os.Stat(filepath.Join(directory, "cwallet.sso"))
	if err == nil && info.Mode().IsRegular() {
		return directory, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return "", nil
}

func readSQLNetWalletSettings(directory string) (string, error) {
	path := filepath.Join(directory, "sqlnet.ora")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTNSNamesSize {
		return "", errors.New("sqlnet.ora must be a regular file no larger than 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTNSNamesSize+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxTNSNamesSize {
		return "", errors.New("sqlnet.ora exceeds 1 MiB")
	}
	return stripTNSComments(string(data)), nil
}

func resolveWalletDirectory(value, directory string) (string, error) {
	value = netScalar(value)
	if value == "" {
		return "", errors.New("Oracle wallet DIRECTORY is empty")
	}
	// Oracle's downloaded ADB sqlnet.ora uses this installation placeholder.
	if value == "?/network/admin" || value == `?\network\admin` {
		return directory, nil
	}
	value, err := prepareWalletDirectory(value, directory)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(directory, value)
	}
	return filepath.Clean(value), nil
}

var walletEnvironmentVariable = regexp.MustCompile(`%[^%\x00\r\n]+%|\$\{[^{}\x00\r\n]+\}|\$[A-Za-z_][A-Za-z0-9_]*`)

// Expand configured references once. Environment values and the effective TNS
// directory are literal paths, not additional templates or shell expressions.
func expandWalletEnvironment(value, directory string) (string, error) {
	var expansionErr error
	value = walletEnvironmentVariable.ReplaceAllStringFunc(value, func(reference string) string {
		if expansionErr != nil {
			return reference
		}
		name := reference[1:]
		if reference[0] == '%' {
			name = reference[1 : len(reference)-1]
		} else if strings.HasPrefix(reference, "${") {
			name = reference[2 : len(reference)-1]
		}
		if directory != "" && (name == "TNS_ADMIN" || (runtime.GOOS == "windows" && strings.EqualFold(name, "TNS_ADMIN"))) {
			return directory
		}
		replacement, found := os.LookupEnv(name)
		if !found || replacement == "" {
			expansionErr = fmt.Errorf("Oracle wallet environment variable %q is not set or is empty", name)
			return reference
		}
		return replacement
	})
	if expansionErr != nil {
		return "", expansionErr
	}
	return value, nil
}

type netAssignment struct{ name, value string }

// netAssignments reads one level only; quoted paths are opaque, even when they
// contain parentheses, #, or text resembling another Oracle Net setting.
func netAssignments(content string) ([]netAssignment, error) {
	var result []netAssignment
	for content = strings.TrimSpace(content); content != ""; content = strings.TrimSpace(content) {
		var entry string
		if content[0] == '(' {
			end, err := scanTNSDescriptor(content, 0)
			if err != nil {
				return nil, err
			}
			entry, content = content[1:end-1], content[end:]
		} else {
			equals := strings.IndexByte(content, '=')
			if equals < 1 {
				return nil, errors.New("invalid Oracle Net assignment")
			}
			start := skipTNSWhitespace(content, equals+1)
			if start < len(content) && content[start] == '(' {
				end, err := scanTNSDescriptor(content, start)
				if err != nil {
					return nil, err
				}
				entry, content = content[:end], content[end:]
			} else {
				end := strings.IndexByte(content, '\n')
				if end < 0 {
					end = len(content)
				}
				entry, content = content[:end], content[end:]
			}
		}
		name, value, ok := strings.Cut(entry, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || !tnsAliasPattern.MatchString(name) {
			return nil, errors.New("invalid Oracle Net assignment")
		}
		if strings.HasPrefix(value, `"`) && (len(value) < 2 || !strings.HasSuffix(value, `"`)) {
			return nil, errors.New("unterminated Oracle Net value")
		}
		result = append(result, netAssignment{name: strings.ToUpper(name), value: value})
	}
	return result, nil
}

func uniqueNetSetting(settings []netAssignment, name string) (string, bool, error) {
	value, found := "", false
	for _, setting := range settings {
		if setting.name != name {
			continue
		}
		if found {
			return "", false, fmt.Errorf("duplicate Oracle Net %s setting", name)
		}
		value, found = setting.value, true
	}
	return value, found, nil
}

func requiredNetSetting(content, name string) (string, error) {
	settings, err := netAssignments(content)
	if err != nil {
		return "", err
	}
	value, found, err := uniqueNetSetting(settings, name)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("Oracle WALLET_LOCATION is missing %s", name)
	}
	return value, nil
}

func netScalar(value string) string {
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func descriptorWallets(content string, depth int) ([]string, error) {
	if depth > 16 {
		return nil, errors.New("Oracle Net descriptor nesting exceeds 16 levels")
	}
	settings, err := netAssignments(content)
	if err != nil {
		return nil, err
	}
	var wallets []string
	for _, setting := range settings {
		switch setting.name {
		case "DESCRIPTION", "DESCRIPTION_LIST":
			values, err := descriptorWallets(setting.value, depth+1)
			if err != nil {
				return nil, err
			}
			wallets = append(wallets, values...)
		case "SECURITY":
			security, err := netAssignments(setting.value)
			if err != nil {
				return nil, err
			}
			value, found, err := uniqueNetSetting(security, "MY_WALLET_DIRECTORY")
			if err != nil {
				return nil, err
			}
			if found {
				wallets = append(wallets, value)
			}
		}
	}
	return wallets, nil
}

// ResolveDescriptorWallet resolves only explicit MY_WALLET_DIRECTORY settings.
// It never opens files or discovers a wallet from the working directory. With a
// relative explicit value, an empty baseDirectory means the current directory.
// All occurrences must resolve to the same native path; quoted text is opaque.
func ResolveDescriptorWallet(descriptor, baseDirectory string) (wallet string, found bool, returnedErr error) {
	directory, err := filepath.Abs(strings.TrimSpace(baseDirectory))
	if err != nil {
		return "", false, err
	}
	wallets, err := descriptorWallets(descriptor, 0)
	if err != nil {
		return "", false, fmt.Errorf("TNS MY_WALLET_DIRECTORY: %w", err)
	}
	if len(wallets) == 0 {
		return "", false, nil
	}
	wallet, err = resolveWalletDirectory(wallets[0], directory)
	if err != nil {
		return "", false, err
	}
	for _, value := range wallets[1:] {
		other, err := resolveWalletDirectory(value, directory)
		if err != nil {
			return "", false, err
		}
		if diagnosticPathKey(other) != diagnosticPathKey(wallet) {
			return "", false, errors.New("TNS descriptor contains conflicting MY_WALLET_DIRECTORY settings")
		}
	}
	return wallet, true, nil
}
func diagnosticPathKey(value string) string {
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		return strings.ToLower(value)
	}
	return value
}
