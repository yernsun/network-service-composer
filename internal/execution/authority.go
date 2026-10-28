package execution

import (
	"context"
	"fmt"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func NewSQLVerifier(store *storage.SQLStore, cfg config.Config, reports *nodecontrol.Reports, keys *security.Keyring) (*Verifier, error) {
	if store == nil || store.Dialect() == storage.SQLite || reports == nil || keys == nil || cfg.Controller.InstanceID == "" || len(cfg.Controller.Nodes) == 0 || len(cfg.Security.SessionKey) < 32 {
		return nil, fmt.Errorf("operation execution requires shared SQL session leases and provisioned node identities")
	}
	state := controlplane.SQLState{Store: store}
	return &Verifier{Store: store, Config: cfg, Reports: reports, Keys: keys, Leader: state, Now: state.DatabaseTime,
		CheckOwner: func(ctx context.Context, key string, owner int64) error {
			return lease.CheckOwner(ctx, store.DB(), store.Dialect(), key, owner)
		}}, nil
}

// FrozenRuntime rejects both legacy tasks without an admission snapshot and a
// runtime that has since been rebound. Neither is evidence of data migration.
func (v *Verifier) FrozenRuntime(op model.DeploymentOperation, current model.Runtime) (model.Runtime, error) {
	snapshot := op.ExecutionIdentity
	if snapshot.Endpoint == "" || snapshot.PoolID == "" || snapshot.Type == "" {
		return model.Runtime{}, ErrFenced
	}
	frozen := current
	frozen.PoolID, frozen.Type, frozen.Endpoint, frozen.CredentialRef, frozen.ClusterID = snapshot.PoolID, snapshot.Type, snapshot.Endpoint, snapshot.CredentialRef, snapshot.ClusterID
	if RuntimeFingerprint(frozen) != RuntimeFingerprint(current) {
		return model.Runtime{}, ErrFenced
	}
	if node := NodeID(snapshot.Endpoint); node != "" {
		binding, err := v.binding(node)
		if err != nil || snapshot.NodeID != binding.NodeID || snapshot.EngineID == "" || snapshot.EngineID != binding.EngineID || snapshot.ClusterID != "" && snapshot.ClusterID != binding.ClusterID {
			return model.Runtime{}, ErrFenced
		}
	}
	return frozen, nil
}

// MarkCommand is saved before a mutating call. If its acknowledgement is lost,
// a new scheduler must inspect effects before it can authorize another attempt.
func MarkCommand(ctx context.Context) error {
	bound, ok := ctx.Value(contextKey{}).(boundExecution)
	if !ok || bound.verifier == nil {
		return ErrFenced
	}
	v := bound.verifier
	if err := v.CheckExecutor(ctx, bound.record); err != nil {
		return err
	}
	record, err := Load(ctx, v.Store, bound.record.Reference.OperationID, bound.record.Reference.Attempt)
	if err != nil {
		return err
	}
	if record.CommandStartedAt != nil {
		return nil
	}
	now, err := v.Now(ctx)
	if err != nil {
		return err
	}
	record.CommandStartedAt = &now
	return Save(ctx, v.Store, record, now)
}
