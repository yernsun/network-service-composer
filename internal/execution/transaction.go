package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"

	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

// CheckStorageAuthority uses only the caller's transaction. Opening another
// connection while holding a task row can deadlock a saturated SQL pool.
func (v *Verifier) CheckStorageAuthority(ctx context.Context, tx *sql.Tx, record Record) error {
	s, ok := v.Store.(*storage.SQLStore)
	if !ok || s.Dialect() == storage.SQLite {
		return ErrFenced
	}
	leader, ok := v.Leader.(controlplane.SQLState)
	if !ok || leader.ValidateTx(ctx, tx, record.Reference.LeaderID, record.Reference.LeaderTerm) != nil {
		return ErrFenced
	}
	ref := record.Reference
	for _, proof := range append(append([]LockProof{}, ref.Locks...), LockProof{Key: ExecutionLock(ref.OperationID, ref.Attempt), Owner: record.ExecutionOwner}, LockProof{Key: nodecontrol.LockKey(ref.Target.NodeID), Owner: ref.Target.CollectorOwner}) {
		if lease.CheckOwnerQuery(ctx, tx, s.Dialect(), proof.Key, proof.Owner) != nil {
			return ErrFenced
		}
	}
	read := func(key, kind string, value any) error {
		var payload string
		if err := tx.QueryRowContext(ctx, storage.BindSQL(s.Dialect(), `SELECT payload FROM control_documents WHERE id=:id AND kind=:kind`), key, kind).Scan(&payload); err != nil {
			return err
		}
		return json.Unmarshal([]byte(payload), value)
	}
	var current Record
	if read(RecordID(ref.OperationID, ref.Attempt), RecordKind, &current) != nil || current.State != "RUNNING" || current.ExecutionOwner != record.ExecutionOwner || !reflect.DeepEqual(current.Reference, ref) {
		return ErrFenced
	}
	var session nodecontrol.Session
	if read("node-session:"+ref.Target.NodeID, "node-session", &session) != nil || session.Generation != ref.Target.CollectorGeneration || session.Owner != ref.Target.CollectorOwner {
		return ErrFenced
	}
	var held int
	if err := tx.QueryRowContext(ctx, storage.BindSQL(s.Dialect(), `SELECT count(*) FROM control_documents WHERE id IN (:barrier,:hold)`), storage.RecoveryBarrierID, storage.RecoveryHoldID("deployment", ref.OperationID)).Scan(&held); err != nil || held != 0 {
		return ErrFenced
	}
	rt := model.Runtime{ID: ref.RuntimeID}
	if err := tx.QueryRowContext(ctx, storage.BindSQL(s.Dialect(), `SELECT pool_id,runtime_type,endpoint,credential_ref,cluster_id,enabled,management_enabled FROM runtimes WHERE id=:id`), ref.RuntimeID).Scan(&rt.PoolID, &rt.Type, &rt.Endpoint, &rt.CredentialRef, &rt.ClusterID, &rt.Enabled, &rt.ManagementEnabled); err != nil || !rt.Enabled || !rt.ManagementEnabled || RuntimeFingerprint(rt) != ref.RuntimeFingerprint {
		return ErrFenced
	}
	return nil
}
