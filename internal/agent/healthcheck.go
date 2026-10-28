package agent

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
)

// Healthcheck observes an already running Agent without preparing generations,
// opening the leader store, or repairing files. A disconnected Controller does
// not invalidate a locally verified last-known-good generation.
func Healthcheck(ctx context.Context, cfg config.Config) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	m := NewGenerationManager(cfg.NGINX)
	directory, state, err := m.healthState(cfg.Agent.PoolID)
	if err != nil {
		return err
	}
	m.state = state
	evidence := m.Observe(ctx)
	if !evidence.LoadingConfirmed || evidence.StartupState != "CONFIRMED" {
		return errors.New("gateway.healthcheck.unconfirmed")
	}
	// This command runs in a separate process from Apply. Never combine evidence
	// across an atomic current-link switch or a concurrent on-disk modification.
	afterDirectory, afterState, err := m.healthState(cfg.Agent.PoolID)
	if err != nil || afterDirectory != directory || afterState != state {
		return errors.New("gateway.healthcheck.generation_changed")
	}
	digest, err := digestGeneration(directory)
	if err != nil || digest != state.RenderedDigest || ctx.Err() != nil {
		return errors.New("gateway.healthcheck.disk_changed")
	}
	return nil
}

func (m *GenerationManager) healthState(poolID string) (string, GenerationState, error) {
	var empty GenerationState
	root, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return "", empty, errors.New("gateway.healthcheck.state_unavailable")
	}
	directory, err := filepath.EvalSymlinks(filepath.Join(root, "current"))
	if err != nil || filepath.Dir(directory) != root {
		return "", empty, errors.New("gateway.healthcheck.current_invalid")
	}
	state, err := readGenerationState(filepath.Join(directory, "generation.json"))
	if err != nil || state.ConfigRevision <= 0 || poolID == "" || state.PoolID != poolID || state.RendererVersion == "" || !validDigest(state.ModelDigest) || !validDigest(state.RenderedDigest) {
		return "", empty, errors.New("gateway.healthcheck.metadata_invalid")
	}
	persisted, err := readGenerationState(filepath.Join(root, "state.json"))
	if err != nil || persisted != state {
		return "", empty, errors.New("gateway.healthcheck.state_mismatch")
	}
	return directory, state, nil
}
