package compat

import (
	"fmt"
	"strings"
	"time"

	"github.com/marcuoli/go-oracle-session/internal/compatdriver/v2/configurations"
)

// V2Endpoint describes one configured endpoint in the order supplied by V2.
type V2Endpoint struct {
	// Protocol is lower case and defaults to tcp when the endpoint omits it.
	Protocol string
	Host     string
	Port     int
}

// V2ConfigSnapshot describes the retained V2 configuration without including
// the DSN, credentials, descriptor text, wallet material, or wallet paths.
type V2ConfigSnapshot struct {
	ConnectTimeout                 time.Duration
	Endpoints                      []V2Endpoint
	ServiceName, SID, InstanceName string
	AuthMode                       string
	TLS, TLSVerify, Wallet         bool
}

// InspectV2Config parses the DSN with the retained V2 configuration parser and
// returns diagnostic values without opening a socket or registering driver
// state. It reads configured wallet files as part of the normal parse. On error
// it returns a zero snapshot and the original parser error for the caller to
// redact as needed.
func InspectV2Config(dsn string) (V2ConfigSnapshot, error) {
	config, err := configurations.ParseConfig(dsn)
	if err != nil {
		return V2ConfigSnapshot{}, err
	}
	endpoints := make([]V2Endpoint, len(config.Servers))
	for i, server := range config.Servers {
		protocol := strings.ToLower(server.Protocol)
		if protocol == "" {
			protocol = "tcp"
		}
		endpoints[i] = V2Endpoint{Protocol: protocol, Host: server.Addr, Port: server.Port}
	}
	return V2ConfigSnapshot{
		ConnectTimeout: config.ConnectTimeout,
		Endpoints:      endpoints,
		ServiceName:    config.ServiceName,
		SID:            config.SID,
		InstanceName:   config.InstanceName,
		AuthMode:       v2AuthMode(config.DBAPrivilege),
		TLS:            config.SSL,
		TLSVerify:      config.SSLVerify,
		Wallet:         config.Wallet != nil,
	}, nil
}

func v2AuthMode(privilege configurations.DBAPrivilege) string {
	switch privilege {
	case configurations.NONE:
		return "normal"
	case configurations.SYSDBA:
		return "SYSDBA"
	case configurations.SYSOPER:
		return "SYSOPER"
	case configurations.SYSASM:
		return "SYSASM"
	case configurations.SYSBACKUP:
		return "SYSBACKUP"
	case configurations.SYSDG:
		return "SYSDG"
	case configurations.SYSKM:
		return "SYSKM"
	case configurations.SYSRAC:
		return "SYSRAC"
	default:
		return fmt.Sprintf("0x%x", int(privilege))
	}
}
