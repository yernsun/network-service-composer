package runtime

import (
	"context"

	"github.com/yernsun/network-service-composer/internal/model"
)

type Provider interface {
	Snapshot(context.Context) (model.RuntimeSnapshot, error)
	Watch(context.Context) (<-chan model.DiscoverySignal, <-chan error)
	Close() error
}

type Factory interface {
	New(context.Context, model.Runtime, string) (Provider, error)
}

type ProviderFactory func(context.Context, model.Runtime, string) (Provider, error)

func (f ProviderFactory) New(ctx context.Context, rt model.Runtime, network string) (Provider, error) {
	return f(ctx, rt, network)
}
