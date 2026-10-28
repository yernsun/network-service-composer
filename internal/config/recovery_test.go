package config

import (
	"strings"
	"testing"
)

func TestRecoveryModeRequiresExplicitMigrationDisableAndIgnoresGatewayListener(t *testing.T) {
	c := validConfig()
	if c.Controller.RecoveryMode {
		t.Fatal("recovery mode enabled by default")
	}
	t.Setenv("NSC_CONTROLLER__RECOVERY_MODE", "true")
	if err := applyEnvironment(&c); err != nil || !c.Controller.RecoveryMode {
		t.Fatal("recovery environment flag did not apply", err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "storage.auto_migrate=false") {
		t.Fatal("recovery allowed startup migrations", err)
	}
	c.Storage.AutoMigrate = false
	c.Server.GRPCListen = "unused in recovery"
	if err := c.Validate(); err != nil {
		t.Fatal("recovery required an unused Gateway listener", err)
	}
	t.Setenv("NSC_CONTROLLER__RECOVERY_MODE", "invalid")
	if err := applyEnvironment(&c); err == nil {
		t.Fatal("invalid recovery flag was silently accepted")
	}
}
