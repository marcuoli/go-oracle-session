package oraclenet

import (
	"net/url"
	"path/filepath"
	"strings"
)

// ConnectInputKind identifies the connection input syntax before resolution.
type ConnectInputKind string

// ConnectInputInvalid, ConnectInputEasyConnect and ConnectInputTNSAlias
// distinguish unrecognized inputs, Easy Connect or URL syntax, and TNS aliases.
const (
	ConnectInputInvalid     ConnectInputKind = "invalid"
	ConnectInputEasyConnect ConnectInputKind = "easy_connect"
	ConnectInputTNSAlias    ConnectInputKind = "tns_alias"
)

// ConnectInput contains fields for connection diagnostics. Alias, TNSAdmin and
// TNSNamesPath apply only to a TNS alias. Paths are caller configuration and
// should follow the caller's disclosure policy.
type ConnectInput struct {
	Kind         ConnectInputKind
	Alias        string
	TNSAdmin     string
	TNSNamesPath string
}

// DescribeConnectInput classifies the input and describes the configured TNS
// file. It does not validate the connection, read the filesystem, or return
// URLs or descriptors. TNSAdmin retains its supplied spelling for display.
func DescribeConnectInput(connect, adminDirectory string) ConnectInput {
	connect = strings.TrimSpace(connect)
	switch {
	case IsEasyConnect(connect):
		return ConnectInput{Kind: ConnectInputEasyConnect}
	case IsAlias(connect):
		input := ConnectInput{Kind: ConnectInputTNSAlias, Alias: connect, TNSAdmin: adminDirectory}
		if strings.TrimSpace(adminDirectory) != "" {
			input.TNSNamesPath = filepath.Join(adminDirectory, tnsNamesFilename)
		}
		return input
	default:
		return ConnectInput{Kind: ConnectInputInvalid}
	}
}

// EasyConnectWalletPaths returns the first explicit wallet value for each
// decoded key spelling in a credential-free Oracle Easy Connect input. Order
// is unspecified across spellings. Unrecognized inputs and omitted wallets
// yield no paths. Invalid wallet settings return the existing preparation
// error. It uses the connection's path expansion without inspecting files or
// exposing other URL options. Callers own path display and redaction.
func EasyConnectWalletPaths(connect, adminDirectory string) ([]string, error) {
	connect = strings.TrimSpace(connect)
	if !IsEasyConnect(connect) {
		return nil, nil
	}
	if !strings.Contains(connect, "://") {
		connect = "oracle://" + strings.TrimPrefix(connect, "//")
	}
	parsed, err := url.Parse(connect)
	if err != nil || !strings.EqualFold(parsed.Scheme, "oracle") || parsed.Host == "" ||
		parsed.User != nil || strings.Trim(parsed.Path, "/") == "" {
		return nil, nil
	}
	if err := PrepareEasyConnectWallet(parsed, adminDirectory); err != nil {
		return nil, err
	}
	var paths []string
	for key, values := range parsed.Query() {
		if strings.EqualFold(key, "wallet") && len(values) > 0 {
			paths = append(paths, values[0])
		}
	}
	return paths, nil
}
