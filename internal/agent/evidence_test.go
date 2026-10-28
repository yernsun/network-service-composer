package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

func TestApplyRefusesToOverwriteExternalChanges(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "generation")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "manual.conf")
	if err := os.WriteFile(file, []byte("external content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("generation", filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	m := NewGenerationManager(config.NGINXConfig{GenerationDir: root})
	m.state = GenerationState{PoolID: "pool_test", ConfigRevision: 1, RenderedDigest: strings.Repeat("a", 64)}
	result, err := m.Apply(context.Background(), &gatewayv2.Snapshot{PoolId: "pool_test", ConfigRevision: 2}, strings.Repeat("b", 64))
	if err == nil || result.FailedPhase != "external-change" {
		t.Fatal(result, err)
	}
	content, err := os.ReadFile(file)
	if err != nil || string(content) != "external content" || m.State().ConfigRevision != 1 {
		t.Fatal("external configuration or last known version changed", err)
	}
}
