package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
)

func TestStandaloneGatewayBindsSharedLeaderStoreBeforeServing(t *testing.T) {
	for _, driver := range []string{"sqlite", "unsupported"} {
		t.Run(driver, func(t *testing.T) {
			root := t.TempDir()
			cfg := config.Defaults()
			cfg.Storage.Driver = driver
			cfg.Storage.DSN = "file:" + filepath.Join(root, "leader.db")
			cfg.NGINX.GenerationDir = filepath.Join(root, "blocked")
			cfg.NGINX.MainConfigPath = filepath.Join(root, "nginx.conf")
			if err := os.WriteFile(cfg.NGINX.GenerationDir, []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
			a := New(cfg, "test", nil)
			err := a.Run(context.Background())
			if err == nil {
				t.Fatal("invalid generation storage was accepted")
			}
			if driver == "unsupported" {
				if !strings.Contains(err.Error(), "leader store") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			state, ok := a.leaderStore.(controlplane.SQLState)
			if !ok {
				t.Fatal("standalone Gateway has no shared leader discovery")
			}
			if err := state.Store.DB().Ping(); err == nil {
				t.Fatal("failed startup leaked the leader database connection")
			}
			if _, err := os.Stat(filepath.Join(root, "leader.db")); !os.IsNotExist(err) {
				t.Fatal("Gateway initialized or migrated a database during startup")
			}
		})
	}
}
