package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

type deletionContextKey struct{}

// Only the deletion executor supplies this capability, after claiming its durable task.
func WithProjectDeletion(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, deletionContextKey{}, id)
}

type ProjectResourceGuard struct {
	ProjectID   string `json:"project_id"`
	OperationID string `json:"operation_id"`
}

type LifecycleResource struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Revision      int64  `json:"revision"`
	Name          string `json:"name"`
	EnvironmentID string `json:"environment_id,omitempty"`
}

type ProjectLifecycleMutation struct {
	ProjectID string
	Expected  int64
	Resources []LifecycleResource
	Gateways  map[string][]string
	Writes    []ControlWrite
	Finishing bool
}

func (s *SQLStore) CheckProjectResource(ctx context.Context, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return s.guardProjectResourceTx(ctx, tx, key)
}

func (s *SQLStore) execProjectMutation(ctx context.Context, key, query string, args ...any) (sql.Result, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, key); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, mapError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return result, nil
}

func (s *SQLStore) guardProjectResourceTx(ctx context.Context, tx *sql.Tx, key string) error {
	return s.guardProjectResourceAccessTx(ctx, tx, key, false)
}

// observation permits readiness evidence for protected system services, while
// retaining deletion fences. It never authorizes changes to their definition.
func (s *SQLStore) guardProjectResourceAccessTx(ctx context.Context, tx *sql.Tx, key string, observation bool) error {
	if err := s.guardDeploymentOperationTx(ctx, tx, ""); err != nil {
		return err
	}
	if key == "" {
		return nil
	}
	var payload, kind string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT payload,kind FROM control_documents WHERE id=:id`), "project-guard:"+key).Scan(&payload, &kind)
	if err == nil {
		var guard ProjectResourceGuard
		if err = json.Unmarshal([]byte(payload), &guard); err != nil {
			return err
		}
		allowed, _ := ctx.Value(deletionContextKey{}).(string)
		if kind == "deleted-project-guard" || allowed == "" || allowed != guard.OperationID {
			return fmt.Errorf("%w: project lifecycle mutation is in progress", ErrConflict)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	projects, err := s.resourceProjectsTx(ctx, tx, key, 0)
	if err != nil {
		return err
	}
	sort.Strings(projects)
	for _, project := range projects {
		if err = s.lockControlOwner(ctx, tx, project); err != nil {
			return err
		}
		var raw string
		query := `SELECT payload FROM control_documents WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		if err = tx.QueryRowContext(ctx, s.bind(query), project).Scan(&raw); err != nil {
			return mapError(err)
		}
		var p struct {
			DeletionID string `json:"deletion_id"`
			System     bool   `json:"system"`
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		if p.System && !observation && ctx.Value(systemBootstrapKey{}) != true {
			return ErrSystemProtected
		}
		allowed, _ := ctx.Value(deletionContextKey{}).(string)
		if p.DeletionID != "" && p.DeletionID != allowed {
			return fmt.Errorf("%w: project lifecycle mutation is in progress", ErrConflict)
		}
	}
	if len(projects) > 0 {
		query := `SELECT kind FROM control_documents WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var current string
		e := tx.QueryRowContext(ctx, s.bind(query), key).Scan(&current)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if current == "deleted-environment" || current == "deleted-project" {
			return ErrConflict
		}
	}
	return nil
}

func (s *SQLStore) resourceProjectsTx(ctx context.Context, tx *sql.Tx, key string, depth int) ([]string, error) {
	if depth > 5 || key == "" {
		return nil, nil
	}
	var kind, parent, raw string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT kind,parent_id,payload FROM control_documents WHERE id=:id`), key).Scan(&kind, &parent, &raw)
	if err == nil {
		if kind == "deleted-environment" || kind == "deleted-project" {
			return nil, ErrConflict
		}
		if kind == "project" {
			return []string{key}, nil
		}
		if kind == "environment" {
			return []string{parent}, nil
		}
		if parent != "" {
			return s.resourceProjectsTx(ctx, tx, parent, depth+1)
		}
		return nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, s.bind(`SELECT payload FROM control_documents WHERE id=:id AND kind='application-environment'`), "application-environment:"+key).Scan(&raw)
	if err == nil {
		var claim struct {
			EnvironmentID string `json:"environment_id"`
		}
		if err = json.Unmarshal([]byte(raw), &claim); err != nil {
			return nil, err
		}
		return s.resourceProjectsTx(ctx, tx, claim.EnvironmentID, depth+1)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var app sql.NullString
	var runtimeID, objectID string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT application_id,runtime_id,runtime_object_id FROM services WHERE id=:id`), key).Scan(&app, &runtimeID, &objectID)
	if err == nil {
		var claimRaw string
		e := tx.QueryRowContext(ctx, s.bind(`SELECT payload FROM control_documents WHERE id=:id AND parent_id=:parent_id AND kind='system-service'`), objectID, runtimeID).Scan(&claimRaw)
		if e == nil {
			var claim SystemService
			if e = json.Unmarshal([]byte(claimRaw), &claim); e != nil {
				return nil, e
			}
			return []string{claim.ProjectID}, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		return s.resourceProjectsTx(ctx, tx, app.String, depth+1)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, s.bind(`SELECT service_id FROM routes WHERE id=:id OR domain_id=:domain_id ORDER BY service_id`), key, key)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id sql.NullString
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		if id.Valid && id.String != "" {
			ids = append(ids, id.String)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		ps, err := s.resourceProjectsTx(ctx, tx, id, depth+1)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if !seen[p] {
				result = append(result, p)
				seen[p] = true
			}
		}
	}
	return result, nil
}

func (s *SQLStore) DeleteEmptyEnvironment(ctx context.Context, key, project string, expected int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err = s.guardProjectResourceTx(ctx, tx, project); err != nil {
		return err
	}
	if err = s.lockControlOwner(ctx, tx, project); err != nil {
		return err
	}
	var raw string
	var revision int64
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT payload,revision FROM control_documents WHERE id=:id AND kind='environment' AND parent_id=:parent_id`), key, project).Scan(&raw, &revision); err != nil {
		return mapError(err)
	}
	if revision != expected {
		return ErrPrecondition
	}
	var env struct {
		ApplicationID string `json:"application_id"`
		Name          string `json:"name"`
	}
	if err = json.Unmarshal([]byte(raw), &env); err != nil {
		return err
	}
	if env.ApplicationID != "" {
		return fmt.Errorf("%w: environment is not empty", ErrConflict)
	}
	var count int
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM control_documents WHERE kind='environment' AND parent_id=:parent_id`), project).Scan(&count); err != nil {
		return err
	}
	if count < 2 {
		return fmt.Errorf("%w: a project must retain an environment", ErrConflict)
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM control_documents WHERE kind='application-environment'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var claim struct {
			EnvironmentID string `json:"environment_id"`
		}
		if err = json.Unmarshal([]byte(raw), &claim); err != nil {
			rows.Close()
			return err
		}
		if claim.EnvironmentID == key {
			rows.Close()
			return ErrConflict
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET kind=:kind,revision=revision+1,updated_at=:updated_at WHERE id=:id`), "deleted-environment", now, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET kind=:kind,revision=revision+1,updated_at=:updated_at WHERE parent_id=:parent_id AND kind IN ('variables','environment-compose','image-policy','image-webhook')`), "deleted-environment-data", now, key); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`DELETE FROM control_documents WHERE id=:id AND kind='environment-name'`), "environment-name:"+project+":"+env.Name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.bind(`UPDATE control_documents SET revision=revision+1,updated_at=:updated_at WHERE id=:id`), now, project); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) MutateProjectLifecycle(ctx context.Context, in ProjectLifecycleMutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	// Discovery holds the runtime row. Acquire that same lock before the project
	// so a snapshot cannot race guard installation or final tombstoning.
	runtimes := map[string]bool{}
	for _, r := range in.Resources {
		if r.Kind != "application" {
			continue
		}
		var runtime string
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT runtime_id FROM applications WHERE id=:id`), r.ID).Scan(&runtime); err != nil {
			return mapError(err)
		}
		runtimes[runtime] = true
	}
	keys := []string{}
	for key := range runtimes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		query := `SELECT id FROM runtimes WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		var runtime string
		if err = tx.QueryRowContext(ctx, s.bind(query), key).Scan(&runtime); err != nil {
			return mapError(err)
		}
	}
	if err = s.lockControlOwner(ctx, tx, in.ProjectID); err != nil {
		return err
	}
	if err = s.guardProjectResourceTx(ctx, tx, in.ProjectID); err != nil {
		return err
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, s.bind(`SELECT revision FROM control_documents WHERE id=:id AND kind='project'`), in.ProjectID).Scan(&revision); err != nil {
		return mapError(err)
	}
	if revision != in.Expected {
		return ErrPrecondition
	}
	apps := []string{}
	for _, r := range in.Resources {
		table := map[string]string{"application": "applications", "service": "services", "domain": "domains", "route": "routes", "pool": "gateway_pools"}[r.Kind]
		if table == "" {
			table = "control_documents"
		}
		query := `SELECT revision FROM ` + table + ` WHERE id=:id`
		if s.dialect != SQLite {
			query += ` FOR UPDATE`
		}
		if err = tx.QueryRowContext(ctx, s.bind(query), r.ID).Scan(&revision); err != nil {
			return mapError(err)
		}
		if r.Revision != revision {
			return ErrPrecondition
		}
		if r.Kind == "application" {
			apps = append(apps, r.ID)
		}
	}
	for _, app := range apps {
		var pending int
		if err = tx.QueryRowContext(ctx, s.bind(`SELECT COUNT(*) FROM deployment_operations WHERE application_id=:application_id AND status IN ('QUEUED','RUNNING')`), app).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return ErrConflict
		}
	}
	for pool, expected := range in.Gateways {
		rows, err := tx.QueryContext(ctx, s.bind(`SELECT id FROM gateways WHERE pool_id=:pool_id ORDER BY id`), pool)
		if err != nil {
			return err
		}
		actual := []string{}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			actual = append(actual, key)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if !slices.Equal(actual, expected) {
			return ErrPrecondition
		}
	}
	if err = s.writeControlTx(ctx, tx, in.Writes); err != nil {
		return err
	}
	if in.Finishing {
		now := time.Now().UTC()
		for _, r := range in.Resources {
			switch r.Kind {
			case "application":
				_, err = tx.ExecContext(ctx, s.bind(`UPDATE applications SET status='DOWN',managed=:managed,revision=revision+1,updated_at=:updated_at WHERE id=:id`), false, now, r.ID)
			case "service":
				_, err = tx.ExecContext(ctx, s.bind(`UPDATE services SET managed=:managed,routing_eligible=:routing_eligible,running_instances=0,ready_instances=0,status='REMOVED',removed_at=COALESCE(removed_at,:removed_at),revision=revision+1,updated_at=:updated_at WHERE id=:id`), false, false, now, now, r.ID)
			case "domain":
				// Retain the certificate and archived domain for history, but stop
				// presenting a deleted project's host as a live certificate binding.
				_, err = tx.ExecContext(ctx, s.bind(`DELETE FROM certificate_domains WHERE domain_id=:domain_id`), r.ID)
			}
			if err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
