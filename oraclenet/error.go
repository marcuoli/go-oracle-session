package oraclenet

import "errors"

// ResolutionReason identifies a configuration failure before any database dial.
type ResolutionReason string

// TNSInvalidAlias and the remaining reasons preserve stable configuration codes.
const (
	TNSInvalidAlias       ResolutionReason = "tns_invalid_alias"
	TNSAdminNotConfigured ResolutionReason = "tns_admin_not_configured"
	TNSAdminUnavailable   ResolutionReason = "tns_admin_unavailable"
	TNSFileUnavailable    ResolutionReason = "tns_file_unavailable"
	TNSFileTooLarge       ResolutionReason = "tns_file_too_large"
	TNSFileMalformed      ResolutionReason = "tns_file_malformed"
	TNSAliasNotFound      ResolutionReason = "tns_alias_not_found"
)

// ErrTNSAliasUnavailable classifies all local alias-resolution failures.
var ErrTNSAliasUnavailable = errors.New("tns_alias_resolution_failed")

// ErrInvalidEasyConnect classifies malformed URL wallet configuration.
var ErrInvalidEasyConnect = errors.New("invalid Oracle Easy Connect URL")

// ResolutionError retains the reason, caller alias/path and native/parser cause.
// Error returns only the machine code; callers own any sanitization/presentation
// of the retained fields and unwrapped cause. No network connection is attempted.
type ResolutionError struct {
	Reason      ResolutionReason
	Alias, Path string
	Cause       error
}

// Error returns only the configuration code, without private paths or causes.
func (err *ResolutionError) Error() string { return string(err.Reason) }

// Unwrap exposes the original cause for errors.Is/As without formatting it.
func (err *ResolutionError) Unwrap() error { return err.Cause }

// Is matches ErrTNSAliasUnavailable for every typed resolution reason.
func (err *ResolutionError) Is(target error) bool { return target == ErrTNSAliasUnavailable }
func newTNSResolutionError(reason ResolutionReason, alias, path string, cause error) error {
	return &ResolutionError{Reason: reason, Alias: alias, Path: path, Cause: cause}
}
