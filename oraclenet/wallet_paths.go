package oraclenet

import (
	"fmt"
	"net/url"
	"runtime"
	"strings"
)

// Both connection setup and diagnostics must validate expanded paths before
// native filepath operations can mistake a foreign path for a relative one.
func prepareWalletDirectory(value, adminDirectory string) (string, error) {
	value, err := expandWalletEnvironment(value, adminDirectory)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		drive := len(value) >= 2 && value[1] == ':' &&
			((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z'))
		if drive || strings.HasPrefix(value, `\\`) {
			return "", fmt.Errorf("Oracle wallet directory %q uses a Windows path on %s; configure a wallet directory for this operating system", value, runtime.GOOS)
		}
	}
	return value, nil
}

// PrepareEasyConnectWallet rewrites only wallet values that expand. Preserve the encoding and order of
// every other URL option, including TLS settings and the wallet password.
// Relative URL wallet paths remain relative to the process working directory.
// The caller owns parsed; nil is invalid. No wallet contents are read.
func PrepareEasyConnectWallet(parsed *url.URL, adminDirectory string) error {
	if parsed == nil {
		return ErrInvalidEasyConnect
	}
	parts := strings.Split(parsed.RawQuery, "&")
	seen := make(map[string]bool)
	for i, part := range parts {
		key, rawValue, _ := strings.Cut(part, "=")
		name, err := url.QueryUnescape(key)
		if err != nil || !strings.EqualFold(name, "wallet") {
			continue
		}
		// Match url.Values and the driver: the first value of each decoded
		// key is used; subsequent values of that same key are ignored.
		if seen[name] {
			continue
		}
		seen[name] = true
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return ErrInvalidEasyConnect
		}
		resolved, err := prepareWalletDirectory(value, adminDirectory)
		if err != nil {
			return err
		}
		if resolved != value {
			parts[i] = key + "=" + url.QueryEscape(resolved)
		}
	}
	parsed.RawQuery = strings.Join(parts, "&")
	return nil
}
