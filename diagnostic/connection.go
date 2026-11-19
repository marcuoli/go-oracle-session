package diagnostic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"
)

// ConnectionKind identifies an observed native-code or standard-library cause.
// It does not infer credentials, retry policy or server state from unknown prose.
type ConnectionKind string

// ConnectionOracle and the other kinds distinguish explicit native codes and
// typed causes. ConnectionUnknown means no supported type/code was observed.
const (
	ConnectionOracle   ConnectionKind = "oracle"
	ConnectionTNS      ConnectionKind = "tns"
	ConnectionDNS      ConnectionKind = "dns"
	ConnectionTCP      ConnectionKind = "tcp"
	ConnectionTimeout  ConnectionKind = "timeout"
	ConnectionTLS      ConnectionKind = "tls"
	ConnectionCanceled ConnectionKind = "canceled"
	ConnectionUnknown  ConnectionKind = "unknown"
)

// ConnectionMessage is one owned redacted cause. Code is an explicit ORA-nnnnn
// or TNS-nnnnn token, otherwise empty. Text may retain host/database identifiers.
type ConnectionMessage struct {
	Kind ConnectionKind `json:"kind"`
	Code string         `json:"code,omitempty"`
	Text string         `json:"text"`
}

var connectionToken = regexp.MustCompile(`\b(?:ORA|TNS)-[0-9]{5}\b`)

// Connection returns ordered, de-duplicated, redacted connection causes without
// modifying the error graph or changing errors.Is/As. It recognizes explicit
// ORA/TNS tokens and concrete standard DNS/TCP/timeout/cancellation/TLS errors;
// other leaf text is retained as unknown after redaction. Wrapper prose is omitted.
// Include all known connection secrets; identifiers and other data can remain.
//
// Bounds match Oracle:64 nodes,32 messages,64 secrets and MaxBytes per leaf/secret
// set. Returned text, including one newline between messages, also fits MaxBytes.
// ErrCapacity returns no prefix. Invalid UTF8 leaves are omitted. Nil returns no
// messages. The function is concurrent-safe when callers do not mutate inputs.
func Connection(err error, secrets ...string) ([]ConnectionMessage, error) {
	pattern, failure := compileSecrets(secrets)
	if failure != nil {
		return nil, failure
	}
	var output []ConnectionMessage
	seen := map[string]bool{}
	total := 0
	appendMessage := func(kind ConnectionKind, code, text string) error {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		key := string(kind) + "\x00" + code + "\x00" + text
		if seen[key] {
			return nil
		}
		size := len(text)
		if len(output) > 0 {
			size++
		}
		if len(output) >= maxMessages || total+size > MaxBytes {
			return ErrCapacity
		}
		seen[key] = true
		total += size
		output = append(output, ConnectionMessage{Kind: kind, Code: code, Text: text})
		return nil
	}
	failure = walkLeaves(err, true, func(leaf error, kind ConnectionKind) error {
		text := leaf.Error()
		if len(text) > MaxBytes {
			return ErrCapacity
		}
		if !utf8.ValidString(text) {
			return nil
		}
		text = redactWithCodes(text, pattern, connectionToken)
		starts := connectionToken.FindAllStringIndex(text, -1)
		if len(starts) == 0 {
			return appendMessage(kind, "", text)
		}
		for i, at := range starts {
			start, end := at[0], len(text)
			if i == 0 {
				start = 0
			}
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			code := text[at[0]:at[1]]
			nativeKind := ConnectionOracle
			if strings.HasPrefix(code, "TNS-") {
				nativeKind = ConnectionTNS
			}
			if err := appendMessage(nativeKind, code, text[start:end]); err != nil {
				return err
			}
		}
		return nil
	})
	if failure != nil {
		return nil, failure
	}
	return output, nil
}

func classifyConnection(err error) ConnectionKind {
	if err == context.Canceled {
		return ConnectionCanceled
	}
	if err == context.DeadlineExceeded || err == os.ErrDeadlineExceeded || err == syscall.ETIMEDOUT {
		return ConnectionTimeout
	}
	switch e := err.(type) {
	case *net.DNSError:
		if e != nil && e.IsTimeout {
			return ConnectionTimeout
		}
		return ConnectionDNS
	case *net.OpError:
		if e != nil && strings.HasPrefix(e.Net, "tcp") {
			return ConnectionTCP
		}
	case *net.AddrError, net.InvalidAddrError:
		return ConnectionTCP
	case tls.RecordHeaderError, *tls.RecordHeaderError, tls.AlertError, *tls.CertificateVerificationError,
		x509.UnknownAuthorityError, *x509.UnknownAuthorityError, x509.HostnameError, *x509.HostnameError,
		x509.CertificateInvalidError, *x509.CertificateInvalidError, x509.SystemRootsError, *x509.SystemRootsError,
		x509.InsecureAlgorithmError, x509.ConstraintViolationError, x509.UnhandledCriticalExtension:
		return ConnectionTLS
	}
	return ConnectionUnknown
}

func inheritKind(parent, current ConnectionKind) ConnectionKind {
	rank := func(kind ConnectionKind) int {
		switch kind {
		case ConnectionCanceled:
			return 5
		case ConnectionTimeout:
			return 4
		case ConnectionTLS:
			return 3
		case ConnectionDNS:
			return 2
		case ConnectionTCP:
			return 1
		}
		return 0
	}
	if rank(current) > rank(parent) {
		return current
	}
	return parent
}
