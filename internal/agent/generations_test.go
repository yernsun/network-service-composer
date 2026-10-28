package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
)

func TestPrepareRecoversStateFromCurrentGenerationCommitPoint(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	poolID := "pool_recovery"
	target := "gen-00000000000000000042-test"
	if err := os.Mkdir(filepath.Join(root, target), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"domains", "upstreams", "system", "certificates"} {
		if err := os.Mkdir(filepath.Join(root, target, directory), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "nginx.conf"), filepath.Join(root, target, generationMainConfigName)); err != nil {
		t.Fatal(err)
	}
	committed := GenerationState{PoolID: poolID, ConfigRevision: 42, ModelDigest: digest,
		RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("c", 64), AppliedAt: time.Now().UTC()}
	if err := writeGenerationStateFile(filepath.Join(root, target), "generation.json", committed); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "current")); err != nil {
		t.Fatal(err)
	}
	if err := writeGenerationState(root, GenerationState{ConfigRevision: 41, ModelDigest: strings.Repeat("b", 64), RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("d", 64), AppliedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	manager := NewGenerationManager(config.NGINXConfig{GenerationDir: root})
	state, err := manager.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if state.PoolID != poolID || state.ConfigRevision != 42 || state.ModelDigest != digest {
		t.Fatalf("recovered state = %#v", state)
	}
	persisted, err := readGenerationState(filepath.Join(root, "state.json"))
	if err != nil || persisted.PoolID != poolID || persisted.ConfigRevision != 42 || persisted.ModelDigest != digest {
		t.Fatalf("persisted recovered state = %#v, err=%v", persisted, err)
	}
}

func TestValidateGenerationPool(t *testing.T) {
	if err := validateGenerationPool(GenerationState{}, "pool_expected"); err != nil {
		t.Fatalf("bootstrap state was rejected: %v", err)
	}
	if err := validateGenerationPool(GenerationState{ConfigRevision: 1}, "pool_expected"); err == nil {
		t.Fatal("identified legacy generation without a pool was accepted")
	}
	if err := validateGenerationPool(GenerationState{PoolID: "pool_other", ConfigRevision: 1}, "pool_expected"); err == nil {
		t.Fatal("generation from another pool was accepted")
	}
	if err := validateGenerationPool(GenerationState{PoolID: "pool_expected", ConfigRevision: 1}, "pool_expected"); err != nil {
		t.Fatalf("matching generation was rejected: %v", err)
	}
}
