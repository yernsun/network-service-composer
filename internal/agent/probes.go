package agent

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"google.golang.org/protobuf/proto"
)

type ProbeRunner struct {
	mu         sync.Mutex
	cancel     context.CancelFunc
	epoch      int64
	revision   int64
	generation uint64
	planDigest [sha256.Size]byte
	hasPlan    bool
	sequences  map[string]int64
	workers    chan struct{}
	outgoing   chan<- *gatewayv2.AgentMessage
}

func NewProbeRunner(workerCount int, outgoing chan<- *gatewayv2.AgentMessage) *ProbeRunner {
	if workerCount < 1 {
		workerCount = 1
	}
	return &ProbeRunner{sequences: make(map[string]int64), workers: make(chan struct{}, workerCount), outgoing: outgoing}
}

func (r *ProbeRunner) SetPlan(parent context.Context, plan *gatewayv2.ProbePlan) error {
	if plan == nil {
		r.Stop()
		return nil
	}
	// Clone before validating and starting goroutines. A gRPC message belongs to
	// the receive loop and must not remain mutable state shared with workers.
	plan = proto.Clone(plan).(*gatewayv2.ProbePlan)
	if err := validateProbePlan(plan); err != nil {
		return err
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode probe plan identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.hasPlan {
		if plan.Epoch < r.epoch || (plan.Epoch == r.epoch && plan.ProbeRevision < r.revision) || plan.ProbeRevision < r.revision {
			r.mu.Unlock()
			return fmt.Errorf("probe plan regressed from epoch %d revision %d to epoch %d revision %d", r.epoch, r.revision, plan.Epoch, plan.ProbeRevision)
		}
		if plan.Epoch == r.epoch && plan.ProbeRevision == r.revision {
			if digest != r.planDigest {
				r.mu.Unlock()
				return fmt.Errorf("probe plan content changed without a new epoch or revision")
			}
			r.mu.Unlock()
			return nil
		}
	}
	r.generation++
	generation := r.generation
	if r.cancel != nil {
		r.cancel()
	}
	if plan.Epoch != r.epoch {
		r.sequences = make(map[string]int64)
		r.epoch = plan.Epoch
	}
	r.revision = plan.ProbeRevision
	r.planDigest = digest
	r.hasPlan = true
	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel
	r.mu.Unlock()
	for _, target := range plan.Targets {
		if target != nil {
			go r.runTarget(ctx, plan.PoolId, plan.Epoch, plan.ProbeRevision, generation, target)
		}
	}
	return nil
}

func (r *ProbeRunner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
}

func validateProbePlan(plan *gatewayv2.ProbePlan) error {
	if plan == nil {
		return fmt.Errorf("probe plan is required")
	}
	if !safeProbeIdentifier(plan.PoolId) {
		return fmt.Errorf("probe plan contains an invalid pool identity")
	}
	if plan.Epoch < 0 || plan.ProbeRevision < 0 {
		return fmt.Errorf("probe plan epoch and revision must not be negative")
	}
	if len(plan.Targets) > 10000 {
		return fmt.Errorf("probe plan contains too many targets")
	}
	serviceIDs := make(map[string]struct{}, len(plan.Targets))
	for index, target := range plan.Targets {
		if target == nil {
			return fmt.Errorf("probe target %d is missing", index)
		}
		if !safeProbeIdentifier(target.ServiceId) {
			return fmt.Errorf("probe target %d contains an invalid service identity", index)
		}
		if _, exists := serviceIDs[target.ServiceId]; exists {
			return fmt.Errorf("probe plan contains duplicate service %q", target.ServiceId)
		}
		serviceIDs[target.ServiceId] = struct{}{}
		if err := validateDockerServiceDNSName(target.DnsName); err != nil {
			return fmt.Errorf("probe target %q: %w", target.ServiceId, err)
		}
		if target.Port < 1 || target.Port > 65535 {
			return fmt.Errorf("probe target %q contains an invalid port", target.ServiceId)
		}
		mode := strings.ToLower(strings.TrimSpace(target.Mode))
		switch mode {
		case "tcp":
		case "http":
			scheme := strings.ToLower(strings.TrimSpace(target.Scheme))
			if scheme != "http" && scheme != "https" {
				return fmt.Errorf("probe target %q contains an invalid HTTP scheme", target.ServiceId)
			}
			if target.Path == "" || !strings.HasPrefix(target.Path, "/") || !safeProbePath(target.Path) {
				return fmt.Errorf("probe target %q contains an invalid HTTP path", target.ServiceId)
			}
		default:
			return fmt.Errorf("probe target %q contains an unsupported mode", target.ServiceId)
		}
		interval := time.Duration(target.IntervalNanos)
		timeout := time.Duration(target.TimeoutNanos)
		if interval < 100*time.Millisecond || interval > 24*time.Hour || timeout <= 0 || timeout > interval {
			return fmt.Errorf("probe target %q contains invalid interval or timeout values", target.ServiceId)
		}
		if target.SuccessThreshold < 1 || target.SuccessThreshold > 1000000 || target.FailureThreshold < 1 || target.FailureThreshold > 1000000 {
			return fmt.Errorf("probe target %q contains invalid readiness thresholds", target.ServiceId)
		}
		if len(target.TargetToken) != sha256.Size*2 {
			return fmt.Errorf("probe target %q contains an invalid target token", target.ServiceId)
		}
		decoded, decodeErr := hex.DecodeString(target.TargetToken)
		if decodeErr != nil || hex.EncodeToString(decoded) != target.TargetToken {
			return fmt.Errorf("probe target %q contains an invalid target token", target.ServiceId)
		}
	}
	return nil
}

func validateDockerServiceDNSName(value string) error {
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return fmt.Errorf("Docker Service DNS name is invalid")
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			continue
		}
		return fmt.Errorf("Docker Service DNS name is invalid")
	}
	return nil
}

func safeProbeIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func safeProbePath(value string) bool {
	if strings.ContainsAny(value, "?#{};\"'\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func (r *ProbeRunner) runTarget(ctx context.Context, poolID string, epoch, revision int64, generation uint64, target *gatewayv2.ProbeTarget) {
	interval := time.Duration(target.IntervalNanos)
	if interval < 100*time.Millisecond {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.runOnce(ctx, poolID, epoch, revision, generation, target)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *ProbeRunner) runOnce(ctx context.Context, poolID string, epoch, revision int64, generation uint64, target *gatewayv2.ProbeTarget) {
	select {
	case r.workers <- struct{}{}:
	case <-ctx.Done():
		return
	}
	started := time.Now()
	success, probeErr := probe(ctx, target)
	latency := time.Since(started)
	<-r.workers
	r.mu.Lock()
	if epoch != r.epoch || generation != r.generation {
		r.mu.Unlock()
		return
	}
	r.sequences[target.ServiceId]++
	sequence := r.sequences[target.ServiceId]
	r.mu.Unlock()
	errorMessage := ""
	if probeErr != nil {
		errorMessage = probeErr.Error()
		if len(errorMessage) > 1024 {
			errorMessage = errorMessage[:1024]
		}
	}
	result := &gatewayv2.ProbeResult{PoolId: poolID, Epoch: epoch, ProbeRevision: revision, ServiceId: target.ServiceId,
		Sequence: sequence, Success: success, LatencyNanos: int64(latency), Error: errorMessage,
		ObservedAtUnixNano: time.Now().UTC().UnixNano(), TargetToken: target.TargetToken}
	message := &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_ProbeResult{ProbeResult: result}}
	select {
	case r.outgoing <- message:
	case <-ctx.Done():
	}
}

func probe(ctx context.Context, target *gatewayv2.ProbeTarget) (bool, error) {
	timeout := time.Duration(target.TimeoutNanos)
	if timeout <= 0 {
		timeout = time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	address := net.JoinHostPort(target.DnsName, fmt.Sprint(target.Port))
	switch strings.ToLower(target.Mode) {
	case "tcp":
		connection, err := (&net.Dialer{}).DialContext(probeCtx, "tcp", address)
		if err != nil {
			return false, err
		}
		return true, connection.Close()
	case "http":
		scheme := strings.ToLower(target.Scheme)
		if scheme == "" {
			scheme = "http"
		}
		if scheme != "http" && scheme != "https" {
			return false, fmt.Errorf("unsupported readiness scheme %q", target.Scheme)
		}
		path := target.Path
		if path == "" {
			path = "/"
		}
		requestURL := (&url.URL{Scheme: scheme, Host: address, Path: path}).String()
		request, err := http.NewRequestWithContext(probeCtx, http.MethodGet, requestURL, nil)
		if err != nil {
			return false, err
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: transport}
		response, err := client.Do(request)
		if err != nil {
			return false, err
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			return false, fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
		}
		return true, nil
	default:
		return false, fmt.Errorf("unsupported readiness mode %q", target.Mode)
	}
}
