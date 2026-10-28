package agent

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"
)

type generationEvidence struct {
	State            GenerationState
	LoadingConfirmed bool
	StartupState     string
	DiskDigest       string
	ObservedAt       time.Time
}

// Observe checks new local requests and the next-start configuration separately.
// Applying and observing share a lock so a probe cannot mix two generations.
func (m *GenerationManager) Observe(ctx context.Context) generationEvidence {
	v := generationEvidence{State: m.State(), StartupState: "UNKNOWN"}
	if !m.operationMu.TryLock() {
		return v
	}
	defer m.operationMu.Unlock()
	v.State = m.State()
	if v.State.ConfigRevision == 0 {
		return v
	}
	probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	v.LoadingConfirmed = waitForGenerationHealth(probeCtx, v.State.ConfigRevision) == nil
	cancel()
	directory, err := filepath.EvalSymlinks(filepath.Join(m.root, "current"))
	if err == nil {
		v.DiskDigest, err = digestGeneration(directory)
	}
	if err != nil || v.DiskDigest != v.State.RenderedDigest || !m.validGenerationMainConfig(directory) {
		v.StartupState = "RISK"
	} else {
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := exec.CommandContext(checkCtx, m.config.Binary, "-t", "-c", generationMainConfigName, "-p", directory).Run()
		cancel()
		if err == nil {
			v.StartupState = "CONFIRMED"
		} else {
			v.StartupState = "RISK"
		}
	}
	v.ObservedAt = time.Now().UTC()
	return v
}
