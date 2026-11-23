package oraclenet

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidConnectStringInput reports malformed, unsupported or ambiguous
// formatting input without disclosing the supplied reference or alias.
var ErrInvalidConnectStringInput = errors.New("invalid Oracle Net connection string input")

// ConnectStrings contains client connection text for one saved route. An empty
// field means that the corresponding format cannot be produced from the input.
type ConnectStrings struct {
	EasyConnect string // Bare and // forms on separate lines for TCP, one form for TCPS.
	TNSNames    string // Complete tnsnames.ora entry when a name and direct route are supplied.
	JDBCThin    string // JDBC Thin URL with credentials supplied separately by the client.
}

// ConnectStringOptions controls optional client configuration templates.
type ConnectStringOptions struct {
	// WalletTemplate adds a <wallet-directory> placeholder to TCPS or alias
	// output. The caller decides from known configuration whether to request it.
	WalletTemplate bool
}

const walletDirectoryPlaceholder = "<wallet-directory>"

// FormatConnectStrings formats one credential-free routing reference with a
// tcp, tcps or oracle URL scheme, an explicit port, and a service or SID.
// The oracle scheme represents TCP. A SERVICE NAME option overrides the path;
// a nonempty SID takes precedence over the service. An opaque TNS alias only
// produces a JDBC alias URL and must be configured on the client.
//
// entryAlias names the generated tnsnames.ora entry; an empty name omits it.
// SID routes have no Easy Connect form. A wallet template requires TCPS or an
// opaque alias. TCPS descriptors request server DN matching. Callers supply
// client trust, any stricter certificate policy, and any wallet
// files separately. TCPS Easy Connect requires support for Easy Connect Plus,
// and JDBC alias TNS_ADMIN in the URL requires JDBC 18.3 or newer.
//
// The formatter performs no alias resolution, file access or network I/O.
// Multi-address references and unsupported URI options return
// ErrInvalidConnectStringInput and an empty result.
func FormatConnectStrings(reference, entryAlias string, options ConnectStringOptions) (ConnectStrings, error) {
	const jdbcPrefix = "jdbc:oracle:thin:@"
	var result ConnectStrings
	reference = strings.TrimSpace(reference)
	if entryAlias != "" && (!IsAlias(entryAlias) || strings.EqualFold(entryAlias, "IFILE")) {
		return result, ErrInvalidConnectStringInput
	}
	if IsAlias(reference) {
		result.JDBCThin = jdbcPrefix + reference
		if options.WalletTemplate {
			result.JDBCThin += "?TNS_ADMIN=" + walletDirectoryPlaceholder
		}
		return result, nil
	}
	if strings.ContainsAny(reference, ";#") {
		return result, ErrInvalidConnectStringInput
	}

	parsed, err := url.Parse(reference)
	if err != nil || parsed.User != nil || parsed.Opaque != "" {
		return result, ErrInvalidConnectStringInput
	}
	protocol := parsed.Scheme
	if protocol == "oracle" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "tcps" || options.WalletTemplate && protocol != "tcps" {
		return result, ErrInvalidConnectStringInput
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return result, ErrInvalidConnectStringInput
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return result, ErrInvalidConnectStringInput
	}
	// A descriptor inserts the host directly into a token. Restrict it to a
	// plain name or an IP literal, with no scope name or descriptor punctuation.
	ip, ipErr := netip.ParseAddr(host)
	if !IsAlias(host) && (ipErr != nil || ip.Zone() != "") {
		return result, ErrInvalidConnectStringInput
	}
	key, name, err := connectData(parsed)
	if err != nil {
		return result, err
	}

	var security, walletQuery string
	if protocol == "tcps" {
		// Older clients recognize the SSL spelling, which newer Oracle Net
		// clients still accept. A wallet path alone does not enable DN matching.
		security = "(SECURITY=(SSL_SERVER_DN_MATCH=YES)"
		if options.WalletTemplate {
			directory := `"` + walletDirectoryPlaceholder + `"`
			walletQuery = "?wallet_location=" + directory
			security += "(WALLET_LOCATION=" + directory + ")"
		}
		security += ")"
	}
	descriptor := func(host string) string {
		return fmt.Sprintf("(DESCRIPTION=(ADDRESS=(PROTOCOL=%s)(HOST=%s)(PORT=%d))(CONNECT_DATA=(%s=%s))%s)",
			strings.ToUpper(protocol), host, port, key, name, security)
	}
	if entryAlias != "" {
		result.TNSNames = entryAlias + " = " + descriptor(host)
	}
	if key == "SID" {
		// Oracle Net descriptors can use a bare IPv6 literal. JDBC URL
		// descriptors require brackets around the same literal.
		if ip.Is6() {
			host = "[" + host + "]"
		}
		result.JDBCThin = jdbcPrefix + descriptor(host)
		return result, nil
	}

	address := net.JoinHostPort(host, strconv.FormatUint(port, 10)) + "/" + name
	if protocol == "tcps" {
		result.EasyConnect = "tcps://" + address + walletQuery
		result.JDBCThin = jdbcPrefix + result.EasyConnect
	} else {
		result.EasyConnect = address + "\n//" + address
		result.JDBCThin = jdbcPrefix + "//" + address
	}
	return result, nil
}

func connectData(parsed *url.URL) (key, value string, err error) {
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", "", ErrInvalidConnectStringInput
	}
	service := strings.Trim(parsed.Path, "/")
	var sid string
	var hasService, hasSID bool
	for key, values := range query {
		if len(values) != 1 {
			return "", "", ErrInvalidConnectStringInput
		}
		switch strings.ToUpper(key) {
		case "SERVICE NAME":
			if hasService {
				return "", "", ErrInvalidConnectStringInput
			}
			service, hasService = values[0], true
		case "SID":
			if hasSID {
				return "", "", ErrInvalidConnectStringInput
			}
			sid, hasSID = values[0], true
		default:
			// Unknown settings may carry credentials or alter routing. The
			// formatter cannot preserve their semantics from a reference.
			return "", "", ErrInvalidConnectStringInput
		}
	}
	if sid != "" && IsAlias(sid) {
		return "SID", sid, nil
	}
	if sid == "" && IsAlias(service) {
		return "SERVICE_NAME", service, nil
	}
	return "", "", ErrInvalidConnectStringInput
}
