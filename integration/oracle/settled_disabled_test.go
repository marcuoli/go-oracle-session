//go:build integration && !settled

package oracle

import (
	"database/sql/driver"
	"testing"

	"github.com/marcuoli/go-oracle-session/session"
)

func configureNativeCancellation(t *testing.T, cfg nativeConfig, connector driver.Connector, opts session.Options) (driver.Connector, session.Options) {
	t.Helper()
	if cfg.SettledCancellation {
		t.Fatal("settled cancellation requires the explicit settled build tag and a compatible shared module")
	}
	return connector, opts
}

func nativeSettledCancellation(t *testing.T, _ nativeConfig, _ bool) {
	t.Helper()
	t.Fatal("settled cancellation profile was not built")
}
