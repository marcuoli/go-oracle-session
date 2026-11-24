// Package oraclenet provides the go-oracle-session Oracle Net API.
// The implementation lives in [github.com/marcuoli/go-oracle-net].
package oraclenet

import (
	"net/url"

	netconfig "github.com/marcuoli/go-oracle-net"
)

type (
	// ConnectStrings contains client connection text for one saved route.
	ConnectStrings = netconfig.ConnectStrings
	// ConnectStringOptions controls connection string formatting and templates.
	ConnectStringOptions = netconfig.ConnectStringOptions
	// ConnectInputKind identifies the connection input syntax before resolution.
	ConnectInputKind = netconfig.ConnectInputKind
	// ConnectInput contains connection input diagnostics.
	ConnectInput = netconfig.ConnectInput
	// TNSName describes an effective alias and its descriptor.
	TNSName = netconfig.TNSName
	// NetInclude retains an unresolved IFILE declaration.
	NetInclude = netconfig.NetInclude
	// TNSNamesConfig contains parsed aliases and include declarations.
	TNSNamesConfig = netconfig.TNSNamesConfig
	// NetSetting contains an Oracle Net assignment and its literal value.
	NetSetting = netconfig.NetSetting
	// SQLNetConfig contains parsed SQLNet assignments and include declarations.
	SQLNetConfig = netconfig.SQLNetConfig
	// SecuritySetting preserves an explicit Oracle Net security setting.
	SecuritySetting = netconfig.SecuritySetting
	// ResolutionReason identifies a failure during local configuration resolution.
	ResolutionReason = netconfig.ResolutionReason
	// ResolutionError reports an alias-resolution failure and retains its cause.
	ResolutionError = netconfig.ResolutionError
)

// ConnectInputInvalid, ConnectInputEasyConnect and ConnectInputTNSAlias classify input syntax.
const (
	ConnectInputInvalid     = netconfig.ConnectInputInvalid
	ConnectInputEasyConnect = netconfig.ConnectInputEasyConnect
	ConnectInputTNSAlias    = netconfig.ConnectInputTNSAlias
)

// TNSInvalidAlias and the remaining reasons preserve stable configuration codes.
const (
	TNSInvalidAlias       = netconfig.TNSInvalidAlias
	TNSAdminNotConfigured = netconfig.TNSAdminNotConfigured
	TNSAdminUnavailable   = netconfig.TNSAdminUnavailable
	TNSFileUnavailable    = netconfig.TNSFileUnavailable
	TNSFileTooLarge       = netconfig.TNSFileTooLarge
	TNSFileMalformed      = netconfig.TNSFileMalformed
	TNSAliasNotFound      = netconfig.TNSAliasNotFound
)

var (
	// ErrInvalidConnectStringInput reports invalid connection formatting input.
	ErrInvalidConnectStringInput = netconfig.ErrInvalidConnectStringInput
	// ErrInvalidEasyConnect reports invalid Easy Connect wallet configuration.
	ErrInvalidEasyConnect = netconfig.ErrInvalidEasyConnect
	// ErrNetConfigCapacity reports configuration above the supported size.
	ErrNetConfigCapacity = netconfig.ErrNetConfigCapacity
	// ErrNetConfigEncoding reports invalid Oracle Net configuration encoding.
	ErrNetConfigEncoding = netconfig.ErrNetConfigEncoding
	// ErrNetConfigSyntax reports unsupported or malformed configuration syntax.
	ErrNetConfigSyntax = netconfig.ErrNetConfigSyntax
	// ErrTNSAliasUnavailable classifies local alias-resolution failures.
	ErrTNSAliasUnavailable = netconfig.ErrTNSAliasUnavailable
)

// FormatConnectStrings formats one credential-free route. See [netconfig.FormatConnectStrings].
func FormatConnectStrings(reference, entryAlias string, options ConnectStringOptions) (ConnectStrings, error) {
	return netconfig.FormatConnectStrings(reference, entryAlias, options)
}

// DescribeConnectInput classifies a connection reference and names its configured TNS file.
func DescribeConnectInput(connect, adminDirectory string) ConnectInput {
	return netconfig.DescribeConnectInput(connect, adminDirectory)
}

// EasyConnectWalletPaths returns explicit wallet paths from a credential-free URL.
func EasyConnectWalletPaths(connect, adminDirectory string) ([]string, error) {
	return netconfig.EasyConnectWalletPaths(connect, adminDirectory)
}

// ParseTNSNames parses supplied Oracle Net alias configuration without filesystem access.
func ParseTNSNames(data []byte) (TNSNamesConfig, error) {
	return netconfig.ParseTNSNames(data)
}

// ParseSQLNet parses supplied SQLNet configuration without filesystem access.
func ParseSQLNet(data []byte) (SQLNetConfig, error) {
	return netconfig.ParseSQLNet(data)
}

// ReadSecuritySettings reads security metadata without enforcing transport policy.
func ReadSecuritySettings(descriptor, adminDirectory string) ([]SecuritySetting, error) {
	return netconfig.ReadSecuritySettings(descriptor, adminDirectory)
}

// IsAlias reports whether value uses the supported alias syntax.
func IsAlias(value string) bool { return netconfig.IsAlias(value) }

// IsEasyConnect distinguishes direct connection syntax from TNS aliases.
func IsEasyConnect(value string) bool { return netconfig.IsEasyConnect(value) }

// ResolveAlias looks up an alias in a bounded, regular tnsnames.ora file.
func ResolveAlias(alias, adminDirectory string) (string, error) {
	return netconfig.ResolveAlias(alias, adminDirectory)
}

// ValidateTNSFiles checks the local TNS configuration directory and file.
func ValidateTNSFiles(alias, adminDirectory string) error {
	return netconfig.ValidateTNSFiles(alias, adminDirectory)
}

// ResolveWallet resolves the wallet path from the descriptor and local configuration.
func ResolveWallet(descriptor, adminDirectory string) (string, error) {
	return netconfig.ResolveWallet(descriptor, adminDirectory)
}

// ResolveDescriptorWallet resolves an explicit descriptor wallet path and reports its presence.
func ResolveDescriptorWallet(descriptor, baseDirectory string) (wallet string, found bool, returnedErr error) {
	return netconfig.ResolveDescriptorWallet(descriptor, baseDirectory)
}

// PrepareEasyConnectWallet prepares the explicit wallet paths in parsed in place.
func PrepareEasyConnectWallet(parsed *url.URL, adminDirectory string) error {
	return netconfig.PrepareEasyConnectWallet(parsed, adminDirectory)
}
