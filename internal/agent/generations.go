package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
)

var ErrOldRevision = errors.New("snapshot revision is not newer than the last known good revision")

const generationMainConfigName = "nginx.conf"

type GenerationState struct {
	PoolID          string    `json:"pool_id"`
	ConfigRevision  int64     `json:"config_revision"`
	ModelDigest     string    `json:"model_digest"`
	RendererVersion string    `json:"renderer_version"`
	RenderedDigest  string    `json:"rendered_digest"`
	AppliedAt       time.Time `json:"applied_at"`
}

type ApplyResult struct {
	ValidationDuration time.Duration
	ReloadDuration     time.Duration
	FailedPhase        string
	Warning            string
}

type GenerationManager struct {
	config       config.NGINXConfig
	root         string
	renderer     *nginxx.Renderer
	state        GenerationState
	process      *exec.Cmd
	startupReady bool
	mu           sync.RWMutex
	operationMu  sync.Mutex
	held         *heldValidation
}

func (m *GenerationManager) mainConfigPath() string {
	if m.config.MainConfigPath != "" {
		return m.config.MainConfigPath
	}
	return filepath.Join(m.root, "nginx.conf")
}

func NewGenerationManager(cfg config.NGINXConfig) *GenerationManager {
	return &GenerationManager{config: cfg, root: cfg.GenerationDir, renderer: nginxx.NewRenderer(cfg)}
}

func (m *GenerationManager) Prepare() (GenerationState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.root, 0o750); err != nil {
		return GenerationState{}, fmt.Errorf("create generation root: %w", err)
	}
	if err := m.renderer.WriteMainConfig(m.mainConfigPath(), filepath.Join(m.root, "nginx.pid")); err != nil {
		return GenerationState{}, fmt.Errorf("write fixed NGINX configuration: %w", err)
	}
	current := filepath.Join(m.root, "current")
	if target, err := os.Readlink(current); err == nil {
		if _, statErr := os.Stat(filepath.Join(m.root, target, "domains")); statErr == nil {
			state, stateErr := readGenerationState(filepath.Join(m.root, target, "generation.json"))
			if stateErr == nil && state.RendererVersion != "" && validDigest(state.ModelDigest) && validDigest(state.RenderedDigest) && m.validGenerationMainConfig(filepath.Join(m.root, target)) {
				if state.RendererVersion != nginxx.RendererVersion {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					output, err := exec.CommandContext(ctx, m.config.Binary, "-t", "-c", generationMainConfigName, "-p", filepath.Join(m.root, target)).CombinedOutput()
					cancel()
					if err != nil {
						return GenerationState{}, fmt.Errorf("previous generation validation failed: %w: %s", err, truncateOutput(output))
					}
				}
				if writeErr := writeGenerationState(m.root, state); writeErr != nil {
					return GenerationState{}, fmt.Errorf("recover generation state: %w", writeErr)
				}
				m.state = state
				return state, nil
			}
			if stateErr != nil && !errors.Is(stateErr, fs.ErrNotExist) {
				return GenerationState{}, fmt.Errorf("read current generation metadata: %w", stateErr)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return GenerationState{}, fmt.Errorf("inspect current generation: %w", err)
	}
	bootstrap := &gatewayv2.Snapshot{PoolId: "bootstrap", ConfigRevision: 0, ChallengeUpstream: "http://127.0.0.1:1"}
	directory, err := os.MkdirTemp(m.root, ".bootstrap-")
	if err != nil {
		return GenerationState{}, err
	}
	if err = m.renderer.Render(bootstrap, directory); err != nil {
		_ = os.RemoveAll(directory)
		return GenerationState{}, fmt.Errorf("render bootstrap generation: %w", err)
	}
	if err = m.linkGenerationMainConfig(directory); err != nil {
		_ = os.RemoveAll(directory)
		return GenerationState{}, fmt.Errorf("link bootstrap main configuration: %w", err)
	}
	name := filepath.Base(directory)
	if err = replaceSymlink(m.root, name); err != nil {
		return GenerationState{}, err
	}
	m.state = GenerationState{RendererVersion: nginxx.RendererVersion}
	if err = writeGenerationState(m.root, m.state); err != nil {
		return GenerationState{}, err
	}
	return m.state, nil
}

func (m *GenerationManager) Start() (<-chan error, error) {
	if m.process != nil && m.process.Process != nil {
		return nil, fmt.Errorf("NGINX is already running")
	}
	cmd := exec.Command(m.config.Binary, "-c", generationMainConfigName, "-p", filepath.Join(m.root, "current"), "-g", "daemon off;")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start NGINX: %w", err)
	}
	m.process = cmd
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		close(done)
	}()
	return done, nil
}

func (m *GenerationManager) Stop(ctx context.Context) error {
	if m.process == nil || m.process.Process == nil {
		return nil
	}
	if err := m.process.Process.Signal(syscall.SIGQUIT); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			_ = m.process.Process.Kill()
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
			if err := m.process.Process.Signal(syscall.Signal(0)); errors.Is(err, os.ErrProcessDone) {
				return nil
			}
		}
	}
}

func (m *GenerationManager) Apply(ctx context.Context, snapshot *gatewayv2.Snapshot, modelDigest string) (ApplyResult, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	result := ApplyResult{FailedPhase: "input"}
	if snapshot == nil || !m.state.acceptsSnapshotRevision(snapshot.ConfigRevision, modelDigest) {
		return result, ErrOldRevision
	}
	if m.state.ConfigRevision > 0 {
		result.FailedPhase = "external-change"
		directory, err := filepath.EvalSymlinks(filepath.Join(m.root, "current"))
		if err != nil {
			return result, fmt.Errorf("current generation cannot be inspected: %w", err)
		}
		digest, err := digestGeneration(directory)
		if err != nil {
			return result, fmt.Errorf("current generation cannot be verified: %w", err)
		}
		if digest != m.state.RenderedDigest || !m.validGenerationMainConfig(directory) {
			return result, errors.New("external configuration changes detected; automatic replacement is blocked")
		}
		result.FailedPhase = "input"
	}
	if snapshot.PoolId == "" {
		return result, fmt.Errorf("snapshot pool identity is empty")
	}
	if m.state.PoolID != "" && m.state.PoolID != snapshot.PoolId {
		return result, fmt.Errorf("snapshot pool identity %q does not match last known good pool %q", snapshot.PoolId, m.state.PoolID)
	}
	if !validDigest(modelDigest) {
		return result, fmt.Errorf("invalid model digest")
	}
	result.FailedPhase = "startup"
	if err := m.waitForStartup(ctx); err != nil {
		return result, err
	}
	result.FailedPhase = "render"
	temporary, err := os.MkdirTemp(m.root, ".candidate-")
	if err != nil {
		return result, fmt.Errorf("create candidate generation: %w", err)
	}
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err = m.renderer.Render(snapshot, temporary); err != nil {
		return result, fmt.Errorf("render candidate generation: %w", err)
	}
	if err = m.linkGenerationMainConfig(temporary); err != nil {
		return result, fmt.Errorf("link candidate main configuration: %w", err)
	}
	currentDirectory := filepath.Join(m.root, "current")
	if err = reuseUnchangedFragments(currentDirectory, temporary); err != nil {
		return result, fmt.Errorf("reuse unchanged fragments: %w", err)
	}
	renderedDigest, err := digestGeneration(temporary)
	if err != nil {
		return result, fmt.Errorf("digest candidate generation: %w", err)
	}
	result.FailedPhase = "validate"
	validateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(validateCtx, m.config.Binary, "-t", "-c", generationMainConfigName, "-p", temporary)
	started := time.Now()
	output, err := command.CombinedOutput()
	result.ValidationDuration = time.Since(started)
	if err != nil {
		return result, fmt.Errorf("nginx -t rejected candidate: %w: %s", err, truncateOutput(output))
	}
	result.FailedPhase = "activate"
	state := GenerationState{PoolID: snapshot.PoolId, ConfigRevision: snapshot.ConfigRevision, ModelDigest: modelDigest,
		RendererVersion: nginxx.RendererVersion, RenderedDigest: renderedDigest, AppliedAt: time.Now().UTC()}
	if err = writeGenerationStateFile(temporary, "generation.json", state); err != nil {
		return result, fmt.Errorf("persist candidate identity: %w", err)
	}
	name := fmt.Sprintf("gen-%020d-%s-%s", snapshot.ConfigRevision, modelDigest[:16], renderedDigest[:16])
	finalDirectory := filepath.Join(m.root, name)
	if err = os.Rename(temporary, finalDirectory); err != nil {
		if _, statErr := os.Stat(finalDirectory); statErr != nil {
			return result, fmt.Errorf("commit candidate generation: %w", err)
		}
		if removeErr := os.RemoveAll(finalDirectory); removeErr != nil {
			return result, fmt.Errorf("replace incomplete candidate generation: %w", removeErr)
		}
		if err = os.Rename(temporary, finalDirectory); err != nil {
			return result, fmt.Errorf("commit replacement generation: %w", err)
		}
		keepTemporary = true
	} else {
		keepTemporary = true
	}
	oldTarget, err := os.Readlink(filepath.Join(m.root, "current"))
	if err != nil {
		return result, fmt.Errorf("read current generation: %w", err)
	}
	if err = replaceSymlink(m.root, name); err != nil {
		return result, err
	}
	result.FailedPhase = "reload"
	started = time.Now()
	if err = m.reload(ctx, snapshot.ConfigRevision); err != nil {
		result.ReloadDuration = time.Since(started)
		rollbackErr := replaceSymlink(m.root, oldTarget)
		if rollbackErr == nil {
			rollbackErr = m.reload(ctx, m.state.ConfigRevision)
		}
		if rollbackErr != nil {
			return result, fmt.Errorf("reload new generation: %w; rollback failed: %v", err, rollbackErr)
		}
		return result, fmt.Errorf("reload new generation: %w (rolled back to last known good)", err)
	}
	result.ReloadDuration = time.Since(started)
	result.FailedPhase = "persist"
	if err = writeGenerationState(m.root, state); err != nil {
		rollbackErr := replaceSymlink(m.root, oldTarget)
		if rollbackErr == nil {
			rollbackErr = m.reload(ctx, m.state.ConfigRevision)
		}
		if rollbackErr != nil {
			return result, fmt.Errorf("persist generation state: %w; rollback failed: %v", err, rollbackErr)
		}
		return result, fmt.Errorf("persist generation state: %w (rolled back to last known good)", err)
	}
	m.state = state
	result.FailedPhase = "prune"
	if err = m.prune(); err != nil {
		// The new state is already active and durably recorded. Rejecting it at
		// this point would make the Controller resend a revision the Agent must
		// correctly reject as old. Keep serving it and surface cleanup failure as
		// an operational warning instead.
		result.Warning = fmt.Sprintf("prune old generations: %v", err)
	}
	result.FailedPhase = ""
	return result, nil
}

func (m *GenerationManager) State() GenerationState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Call under operationMu before changing the active generation. Start only
// launches the process; NGINX must finish reading the previous generation and
// write its own PID before any validation/reload can touch those same files.
func (m *GenerationManager) waitForStartup(ctx context.Context) error {
	if m.process == nil || m.process.Process == nil || m.startupReady {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		pid, err := os.ReadFile(filepath.Join(m.root, "nginx.pid"))
		if err == nil && strings.TrimSpace(string(pid)) == strconv.Itoa(m.process.Process.Pid) {
			if err := waitForGenerationHealth(ctx, m.state.ConfigRevision); err != nil {
				return fmt.Errorf("wait for initial NGINX generation: %w", err)
			}
			m.startupReady = true
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for initial NGINX PID: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (m *GenerationManager) reload(ctx context.Context, expectedRevision int64) error {
	reloadCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(reloadCtx, m.config.Binary, "-s", "reload", "-c", generationMainConfigName, "-p", filepath.Join(m.root, "current"))
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncateOutput(output))
	}
	return waitForGenerationHealth(reloadCtx, expectedRevision)
}

// NGINX resolves relative includes from the directory of the file passed to
// -c, not from an unrelated absolute main-config path. Each immutable
// generation therefore carries only a symlink to the single fixed main
// configuration. Running with `-p <generation> -c nginx.conf` makes the same
// bytes resolve that generation's fragments during both nginx -t and reload.
func (m *GenerationManager) linkGenerationMainConfig(directory string) error {
	target, err := filepath.Abs(m.mainConfigPath())
	if err != nil {
		return err
	}
	return os.Symlink(target, filepath.Join(directory, generationMainConfigName))
}

func (m *GenerationManager) validGenerationMainConfig(directory string) bool {
	target, err := os.Readlink(filepath.Join(directory, generationMainConfigName))
	if err != nil {
		return false
	}
	wanted, err := filepath.Abs(m.mainConfigPath())
	return err == nil && target == wanted
}

func waitForGenerationHealth(ctx context.Context, expectedRevision int64) error {
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 500 * time.Millisecond}
	wanted := strconv.FormatInt(expectedRevision, 10)
	var last string
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/_nsc/generation-health", nil)
		if err != nil {
			return err
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			last = response.Header.Get("X-NSC-Config-Revision")
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && last == wanted {
				return nil
			}
		} else {
			last = requestErr.Error()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("new NGINX workers did not load config revision %d: last observation %q: %w", expectedRevision, last, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (m *GenerationManager) prune() error {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "gen-") {
			names = append(names, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	keep := m.config.KeepGenerations
	if keep < 1 {
		keep = 3
	}
	current, _ := os.Readlink(filepath.Join(m.root, "current"))
	kept := 0
	for _, name := range names {
		if name == current || kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(m.root, name)); err != nil {
			return err
		}
	}
	return nil
}

func replaceSymlink(root, target string) error {
	if filepath.Base(target) != target || target == "." || target == "" {
		return fmt.Errorf("invalid generation symlink target")
	}
	temporary := filepath.Join(root, ".current-next")
	_ = os.Remove(temporary)
	if err := os.Symlink(target, temporary); err != nil {
		return fmt.Errorf("create generation symlink: %w", err)
	}
	if err := os.Rename(temporary, filepath.Join(root, "current")); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("activate generation symlink: %w", err)
	}
	return syncDirectory(root)
}

func writeGenerationState(root string, state GenerationState) error {
	return writeGenerationStateFile(root, "state.json", state)
}

func writeGenerationStateFile(root, name string, state GenerationState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if filepath.Base(name) != name || name == "." || name == "" {
		return fmt.Errorf("invalid generation state file name")
	}
	temporary := filepath.Join(root, "."+name+"-next")
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err = os.Rename(temporary, filepath.Join(root, name)); err != nil {
		return err
	}
	return syncDirectory(root)
}

func readGenerationState(path string) (GenerationState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return GenerationState{}, err
	}
	var state GenerationState
	if err = json.Unmarshal(data, &state); err != nil {
		return GenerationState{}, fmt.Errorf("decode generation state: %w", err)
	}
	return state, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func reuseUnchangedFragments(current, candidate string) error {
	if _, err := os.Stat(current); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(candidate, path)
		if err != nil || relative == "generation.json" {
			return err
		}
		oldPath := filepath.Join(current, relative)
		oldData, err := os.ReadFile(oldPath)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		newData, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(oldData, newData) {
			return err
		}
		oldInfo, err := os.Stat(oldPath)
		if err != nil {
			return err
		}
		newInfo, err := os.Stat(path)
		if err != nil || oldInfo.Mode().Perm() != newInfo.Mode().Perm() {
			return err
		}
		if err = os.Remove(path); err != nil {
			return err
		}
		return os.Link(oldPath, path)
	})
}

func digestGeneration(directory string) (string, error) {
	var paths []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if relative != "generation.json" {
				paths = append(paths, relative)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(directory, relative))
		if err != nil {
			return "", err
		}
		info, err := os.Stat(filepath.Join(directory, relative))
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(info.Mode().Perm().String()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func truncateOutput(value []byte) string {
	const limit = 4096
	value = []byte(strings.TrimSpace(string(value)))
	if len(value) > limit {
		value = value[:limit]
	}
	return string(value)
}
