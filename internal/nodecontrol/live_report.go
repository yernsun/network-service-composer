package nodecontrol

import (
	"context"
	"errors"
	"time"
)

// ReadLive requires complete, fresh inventory and the current collector lease.
// A stopped collector cannot establish live execution capability.
func (s Reports) ReadLive(ctx context.Context, node string) (Report, error) {
	report, err := s.Read(ctx, node)
	if err != nil {
		return report, err
	}
	return report, s.liveReportOwner(ctx, node, report)
}

// ReadControllerHealth separates Controller forwarding/scheduling evidence from
// Docker inventory. Missing quorum cannot make the API disappear or prove that
// an otherwise reachable Controller has stopped. Execution still uses ReadLive.
func (s Reports) ReadControllerHealth(ctx context.Context, node string) (Report, error) {
	report, err := s.Read(ctx, node)
	if err != nil && !errors.Is(err, ErrIncomplete) {
		return report, err
	}
	health := report.Observation.Health
	if health == nil || health.At.IsZero() || report.Observation.At.Sub(health.At) > 5*time.Second || health.At.Sub(report.Observation.At) > 5*time.Second {
		return report, ErrStale
	}
	return report, s.liveReportOwner(ctx, node, report)
}

func (s Reports) liveReportOwner(ctx context.Context, node string, report Report) error {
	session, _, err := s.session(ctx, node)
	if err != nil {
		return err
	}
	if session.Generation != report.Generation || session.Owner <= 0 || s.CheckOwner == nil {
		return ErrStale
	}
	if err = s.CheckOwner(ctx, LockKey(node), session.Owner); err != nil {
		return ErrStale
	}
	return nil
}
