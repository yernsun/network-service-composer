// Package controlplane coordinates the single scheduling authority shared by
// Controller instances. A database lock and a durable term serve different
// purposes: the lock elects an owner; the term fences delayed work.
package controlplane

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/storage"
)

const leaderKey = "controller:leader"

var ErrNotLeader = errors.New("controller leadership is unavailable or stale")

type Identity struct {
	ID          string `json:"instance_id"`
	URL         string `json:"internal_url"`
	LockOwner   int64  `json:"lock_owner,omitempty"`
	GRPCAddress string `json:"grpc_address,omitempty"`
}

type State struct {
	Identity
	Term      int64     `json:"term"`
	ExpiresAt time.Time `json:"expires_at"`
	Revision  int64     `json:"-"`
}

// StateStore uses the database clock for expiry and CAS for every transition.
// Callers must hold the cluster's dedicated database lock before Claim/Renew.
type StateStore interface {
	Current(context.Context) (State, error)
	Claim(context.Context, Identity, time.Duration) (State, error)
	Renew(context.Context, State, time.Duration) (State, error)
	Revoke(context.Context, State) error
	Validate(context.Context, string, int64) error
}

type SQLState struct{ Store *storage.SQLStore }

func (s SQLState) ValidateTx(ctx context.Context, tx *sql.Tx, id string, term int64) error {
	var payload string
	if err := tx.QueryRowContext(ctx, storage.BindSQL(s.Store.Dialect(), `SELECT payload FROM control_documents WHERE id=:id AND kind='controller-leader' FOR SHARE`), leaderKey).Scan(&payload); err != nil {
		return ErrNotLeader
	}
	var current State
	if json.Unmarshal([]byte(payload), &current) != nil || current.ID != id || current.Term != term || current.LockOwner <= 0 {
		return ErrNotLeader
	}
	query := "SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint"
	if s.Store.Dialect() == storage.MySQL {
		query = "SELECT UNIX_TIMESTAMP(CURRENT_TIMESTAMP)"
	}
	var seconds int64
	if tx.QueryRowContext(ctx, query).Scan(&seconds) != nil || !time.Unix(seconds, 0).Before(current.ExpiresAt) {
		return ErrNotLeader
	}
	return lease.CheckOwnerQuery(ctx, tx, s.Store.Dialect(), leaderKey, current.LockOwner)
}

// DatabaseTime is shared by leadership and node-observation expiry checks.
func (s SQLState) DatabaseTime(ctx context.Context) (time.Time, error) { return s.now(ctx) }

func (s SQLState) now(ctx context.Context) (time.Time, error) {
	query := "SELECT CAST(strftime('%s','now') AS INTEGER)"
	switch s.Store.Dialect() {
	case storage.Postgres:
		query = "SELECT FLOOR(EXTRACT(EPOCH FROM clock_timestamp()))::bigint"
	case storage.MySQL:
		query = "SELECT UNIX_TIMESTAMP(CURRENT_TIMESTAMP)"
	}
	var seconds int64
	err := s.Store.DB().QueryRowContext(ctx, query).Scan(&seconds)
	return time.Unix(seconds, 0).UTC(), err
}

func (s SQLState) Current(ctx context.Context) (State, error) {
	d, err := s.Store.GetControlDocument(ctx, leaderKey)
	if errors.Is(err, storage.ErrNotFound) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	if d.Kind != "controller-leader" {
		return State{}, ErrNotLeader
	}
	var v State
	if err = json.Unmarshal(d.Payload, &v); err != nil {
		return State{}, err
	}
	v.Revision = d.Revision
	return v, nil
}

func (s SQLState) save(ctx context.Context, v State) (State, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return State{}, err
	}
	err = s.Store.WriteControlDocuments(ctx, []storage.ControlWrite{{
		Document: storage.ControlDocument{ID: leaderKey, Kind: "controller-leader", Payload: body, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, Expected: v.Revision,
	}})
	if err != nil {
		return State{}, err
	}
	v.Revision++
	return v, nil
}

func (s SQLState) Claim(ctx context.Context, who Identity, ttl time.Duration) (State, error) {
	if who.ID == "" || ttl < time.Second {
		return State{}, ErrNotLeader
	}
	v, err := s.Current(ctx)
	if err != nil {
		return State{}, err
	}
	if v.Term < 0 || v.Term == math.MaxInt64 {
		return State{}, fmt.Errorf("controller term exhausted")
	}
	now, err := s.now(ctx)
	if err != nil {
		return State{}, err
	}
	v.Identity, v.Term, v.ExpiresAt = who, v.Term+1, now.Add(ttl)
	return s.save(ctx, v)
}

func (s SQLState) Renew(ctx context.Context, v State, ttl time.Duration) (State, error) {
	if ttl < time.Second {
		return State{}, ErrNotLeader
	}
	if err := s.Validate(ctx, v.ID, v.Term); err != nil {
		return State{}, err
	}
	now, err := s.now(ctx)
	if err != nil {
		return State{}, err
	}
	v.ExpiresAt = now.Add(ttl)
	return s.save(ctx, v)
}

func (s SQLState) Revoke(ctx context.Context, v State) error {
	v.ExpiresAt = time.Time{}
	_, err := s.save(ctx, v)
	return err
}

func (s SQLState) Validate(ctx context.Context, owner string, term int64) error {
	v, err := s.Current(ctx)
	if err != nil {
		return err
	}
	now, err := s.now(ctx)
	if err != nil {
		return err
	}
	if owner == "" || term <= 0 || v.ID != owner || v.Term != term || !now.Before(v.ExpiresAt) {
		return ErrNotLeader
	}
	if err := lease.CheckOwner(ctx, s.Store.DB(), s.Store.Dialect(), leaderKey, v.LockOwner); err != nil {
		return fmt.Errorf("%w: %v", ErrNotLeader, err)
	}
	return nil
}
