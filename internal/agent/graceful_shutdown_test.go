package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
)

func TestGenerationManagerStopsNGINXWithGracefulQuit(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "signal")
	binary := filepath.Join(root, "nginx-fixture")
	quoted := strconv.Quote(marker)
	script := "#!/bin/sh\ntrap 'printf quit > " + quoted + "; exit 0' QUIT\nprintf ready > " + quoted + "\nwhile :; do sleep 0.1; done\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	manager := NewGenerationManager(config.NGINXConfig{Binary: binary, GenerationDir: root})
	if _, err := manager.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.Stop(ctx)
	})
	deadline := time.Now().Add(time.Second)
	for {
		contents, err := os.ReadFile(marker)
		if err == nil && string(contents) == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("NGINX fixture did not start: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(marker)
	if err != nil || !strings.HasPrefix(string(contents), "quit") {
		t.Fatalf("NGINX did not receive graceful QUIT: %q, %v", contents, err)
	}
}
