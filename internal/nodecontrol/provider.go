package nodecontrol

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

// Provider refreshes the immutable client for each discovery snapshot. It does
// not expose Docker's HTTP API or confuse a recorded observation with a live RPC.
type Provider struct {
	Reports   *Reports
	Runtime   model.Runtime
	Network   string
	Readiness model.ReadinessPolicy
}

func (p *Provider) Snapshot(ctx context.Context) (model.RuntimeSnapshot, error) {
	cli, err := p.Reports.Client(ctx, p.Runtime)
	if err != nil {
		return model.RuntimeSnapshot{}, err
	}
	provider := engine.NewWithClient(cli, p.Runtime, p.Network, p.Readiness)
	defer provider.Close()
	return provider.Snapshot(ctx)
}

func (p *Provider) Watch(ctx context.Context) (<-chan model.DiscoverySignal, <-chan error) {
	signals, failures := make(chan model.DiscoverySignal, 1), make(chan error)
	go func() {
		defer close(signals)
		defer close(failures)
		var previous time.Time
		for pause(ctx, 5*time.Second) {
			cli, err := p.Reports.Client(ctx, p.Runtime)
			if err != nil || !cli.ObservedAt().Equal(previous) {
				if err == nil {
					previous = cli.ObservedAt()
				}
				select {
				case signals <- model.DiscoverySignal{RuntimeID: p.Runtime.ID, Kind: "node-observation", At: time.Now().UTC()}:
				default:
				}
			}
		}
	}()
	return signals, failures
}

func (p *Provider) Close() error          { return nil }
func (p *Provider) UsesNodeReports() bool { return true }
