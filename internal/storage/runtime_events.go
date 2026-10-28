package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

type TimelineFilter struct {
	ProjectID, EnvironmentID, RuntimeID, ApplicationID, ServiceID, Category string
	GapRuntimeIDs                                                           []string
	BeforeAt                                                                time.Time
	BeforeID                                                                string
}

func eventCategory(signal model.DiscoverySignal) string {
	switch {
	case signal.ActorType == "container" && signal.Kind == "die" && signal.ExitCode != nil && *signal.ExitCode == 0:
		return "completed"
	case signal.ActorType == "container" && (signal.Kind == "die" || signal.Kind == "oom" || signal.Kind == "kill"):
		return "fault"
	case signal.ActorType == "container" && signal.Kind == "restart":
		return "restart"
	case signal.ActorType == "container" && (signal.Kind == "health_status: healthy" || signal.Kind == "health_status: unhealthy"):
		return "health"
	case signal.ActorType == "container" && (signal.Kind == "start" || signal.Kind == "stop" || signal.Kind == "destroy" || signal.Kind == "create"):
		return "lifecycle"
	case (signal.ActorType == "task" || signal.ActorType == "service") && (signal.Kind == "create" || signal.Kind == "update" || signal.Kind == "remove"):
		return "lifecycle"
	default:
		return ""
	}
}

func eventIdentity(runtimeID string, signal model.DiscoverySignal) string {
	value := runtimeID + "\x00" + signal.ActorType + "\x00" + signal.ExternalID + "\x00" + signal.Kind + "\x00" + signal.Status + "\x00" + signal.At.UTC().Format(time.RFC3339Nano)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *SQLStore) eventRuntimes(ctx context.Context, source model.RuntimeEventSource) ([]string, error) {
	if source.RuntimeID != "" {
		return []string{source.RuntimeID}, nil
	}
	if source.ClusterID == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT id FROM runtimes WHERE cluster_id=:cluster_id AND enabled=:enabled ORDER BY id`), source.ClusterID, true)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *SQLStore) sourceHeartbeat(ctx context.Context, tx *sql.Tx, source model.RuntimeEventSource, at time.Time, state, reason string) error {
	var previous time.Time
	var previousState string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT last_heartbeat_at,state FROM runtime_event_sources WHERE id=:id`), source.ID).Scan(&previous, &previousState)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO runtime_event_sources(id,runtime_id,cluster_id,node_id,engine_id,state,first_seen_at,last_heartbeat_at,last_error) VALUES(:id,:runtime_id,:cluster_id,:node_id,:engine_id,:state,:first_seen_at,:last_heartbeat_at,:last_error)`),
			source.ID, source.RuntimeID, source.ClusterID, source.NodeID, source.EngineID, state, at, at, reason)
		return mapError(err)
	}
	if err != nil {
		return err
	}
	if state != "collecting" && previousState == "collecting" {
		if err := s.insertGap(ctx, tx, source, previous, nil, reason, at); err != nil {
			return err
		}
	}
	if state == "collecting" && previousState != "collecting" {
		result, err := tx.ExecContext(ctx, s.bind(`UPDATE runtime_events SET gap_end_at=:gap_end_at,observed_at=:observed_at WHERE source_id=:source_id AND category='gap' AND gap_end_at IS NULL`), at, at, source.ID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 && at.After(previous) {
			if err := s.insertGap(ctx, tx, source, previous, &at, previousState, at); err != nil {
				return err
			}
		}
	} else if state == "collecting" && at.Sub(previous) > 30*time.Second {
		// Docker replay cannot certify complete coverage after a missed heartbeat.
		if err := s.insertGap(ctx, tx, source, previous, &at, "heartbeat_missing", at); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, s.bind(`UPDATE runtime_event_sources SET runtime_id=:runtime_id,cluster_id=:cluster_id,node_id=:node_id,engine_id=:engine_id,state=:state,last_heartbeat_at=:last_heartbeat_at,last_error=:last_error WHERE id=:id`),
		source.RuntimeID, source.ClusterID, source.NodeID, source.EngineID, state, at, reason, source.ID)
	return err
}

func (s *SQLStore) insertGap(ctx context.Context, tx *sql.Tx, source model.RuntimeEventSource, start time.Time, end *time.Time, reason string, observedAt time.Time) error {
	key := eventIdentity(source.RuntimeID, model.DiscoverySignal{ActorType: "gap", ExternalID: source.ID, Kind: "collection_gap", At: start})
	gap := model.RuntimeEvent{ID: "re_" + key[:32], EventKey: key, SourceID: source.ID, RuntimeID: source.RuntimeID,
		NodeID: source.NodeID, ActorType: "gap", ActorID: source.ID, Action: "collection_gap", Category: "gap",
		Status: reason, OccurredAt: start, ObservedAt: observedAt, GapEndAt: end}
	return s.insertRuntimeEvent(ctx, tx, gap)
}

const runtimeEventColumns = `id,event_key,source_id,runtime_id,service_id,application_id,project_id,environment_id,node_id,instance_id,actor_type,actor_id,action,category,status,exit_code,oom_killed,occurred_at,observed_at,gap_end_at`

func (s *SQLStore) insertRuntimeEvent(ctx context.Context, tx *sql.Tx, event model.RuntimeEvent) error {
	query := `INSERT INTO runtime_events (` + runtimeEventColumns + `) VALUES (` + columnPlaceholders(runtimeEventColumns) + `)`
	if s.dialect == MySQL {
		query += ` ON DUPLICATE KEY UPDATE id=id`
	} else {
		query += ` ON CONFLICT(event_key) DO NOTHING`
	}
	_, err := tx.ExecContext(ctx, s.bind(query), event.ID, event.EventKey, event.SourceID, event.RuntimeID, event.ServiceID, event.ApplicationID,
		event.ProjectID, event.EnvironmentID, event.NodeID, event.InstanceID, event.ActorType, event.ActorID, event.Action,
		event.Category, event.Status, ptrInt(event.ExitCode), event.OOMKilled, event.OccurredAt, event.ObservedAt, ptrTime(event.GapEndAt))
	return mapError(err)
}

func ptrInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *SQLStore) eventService(ctx context.Context, tx *sql.Tx, runtimeID string, signal model.DiscoverySignal) (string, string, error) {
	objectID := signal.ServiceObjectID
	if signal.ActorType == "service" && objectID == "" {
		objectID = signal.ExternalID
	}
	var serviceID, applicationID string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT id,COALESCE(application_id,'') FROM services WHERE runtime_id=:runtime_id AND
		((service_key=:service_key AND :service_key_present<>'') OR (runtime_object_id=:runtime_object_id AND :runtime_object_id_present<>'') OR id=(SELECT service_id FROM service_instances WHERE external_id=:external_id LIMIT 1)) ORDER BY id LIMIT 1`),
		runtimeID, signal.ServiceKey, signal.ServiceKey, objectID, objectID, signal.ExternalID).Scan(&serviceID, &applicationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return serviceID, applicationID, err
}

func (s *SQLStore) eventInstance(ctx context.Context, tx *sql.Tx, serviceID string, signal model.DiscoverySignal) (string, error) {
	if signal.ActorType == "service" {
		return "", nil
	}
	for _, externalID := range []string{signal.TaskID, signal.ExternalID} {
		if externalID == "" {
			continue
		}
		var instanceID string
		err := tx.QueryRowContext(ctx, s.bind(`SELECT id FROM service_instances WHERE service_id=:service_id AND external_id=:external_id LIMIT 1`), serviceID, externalID).Scan(&instanceID)
		if err == nil {
			return instanceID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return "", nil
}

func (s *SQLStore) eventScope(ctx context.Context, tx *sql.Tx, applicationID string) (string, string, error) {
	if applicationID == "" {
		return "", "", nil
	}
	var raw string
	err := tx.QueryRowContext(ctx, s.bind(`SELECT payload FROM control_documents WHERE kind='application-environment' AND parent_id=:parent_id`), applicationID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	var claim struct {
		EnvironmentID string `json:"environment_id"`
	}
	if err := json.Unmarshal([]byte(raw), &claim); err != nil {
		return "", "", err
	}
	var projectID string
	err = tx.QueryRowContext(ctx, s.bind(`SELECT parent_id FROM control_documents WHERE kind='environment' AND id=:id`), claim.EnvironmentID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return projectID, claim.EnvironmentID, err
}

func (s *SQLStore) insertOperationWithScopeTx(ctx context.Context, tx *sql.Tx, value *model.DeploymentOperation) error {
	if err := s.captureOperationIdentity(ctx, tx, value); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.bind(`INSERT INTO deployment_operations (`+operationColumns+`) VALUES (`+columnPlaceholders(operationColumns)+`)`), operationArgs(value)...); err != nil {
		return mapError(err)
	}
	if value.ApplicationID == "" && value.ServiceID == "" {
		return nil
	}
	applicationID := value.ApplicationID
	if applicationID == "" && value.ServiceID != "" {
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT COALESCE(application_id,'') FROM services WHERE id=:id`), value.ServiceID).Scan(&applicationID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	projectID, environmentID, err := s.eventScope(ctx, tx, applicationID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET project_id=:project_id,environment_id=:environment_id,scope_snapshot=:scope_snapshot WHERE id=:id`), projectID, environmentID, true, value.ID)
	return err
}

func (s *SQLStore) RecordRuntimeEvents(ctx context.Context, source model.RuntimeEventSource, signals []model.DiscoverySignal, at time.Time) error {
	if source.ID == "" || (source.RuntimeID == "" && source.ClusterID == "") {
		return fmt.Errorf("runtime event source identity is incomplete")
	}
	runtimes, err := s.eventRuntimes(ctx, source)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := s.sourceHeartbeat(ctx, tx, source, at, "collecting", ""); err != nil {
		return err
	}
	for _, signal := range signals {
		category := eventCategory(signal)
		if category == "" || signal.ExternalID == "" || signal.At.IsZero() {
			continue
		}
		for _, runtimeID := range runtimes {
			serviceID, applicationID, err := s.eventService(ctx, tx, runtimeID, signal)
			if err != nil {
				return err
			}
			if serviceID == "" {
				continue // Only recognized Services enter the business timeline.
			}
			projectID, environmentID, err := s.eventScope(ctx, tx, applicationID)
			if err != nil {
				return err
			}
			key := eventIdentity(runtimeID, signal)
			instanceID, err := s.eventInstance(ctx, tx, serviceID, signal)
			if err != nil {
				return err
			}
			nodeID := source.NodeID
			if signal.NodeID != "" {
				nodeID = signal.NodeID
			}
			status := signal.Status
			if status == "" {
				status = "observed"
			}
			event := model.RuntimeEvent{ID: "re_" + key[:32], EventKey: key, SourceID: source.ID, RuntimeID: runtimeID,
				ServiceID: serviceID, ApplicationID: applicationID, ProjectID: projectID, EnvironmentID: environmentID,
				NodeID: nodeID, InstanceID: instanceID, ActorType: signal.ActorType, ActorID: signal.ExternalID,
				Action: signal.Kind, Category: category, Status: status, ExitCode: signal.ExitCode,
				OOMKilled: signal.Kind == "oom", OccurredAt: signal.At.UTC(), ObservedAt: at.UTC()}
			if err := s.insertRuntimeEvent(ctx, tx, event); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) MarkRuntimeEventSource(ctx context.Context, source model.RuntimeEventSource, reason string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer rollbackUnlessCommitted(tx, &committed)
	if err := s.sourceHeartbeat(ctx, tx, source, at, "unavailable", reason); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *SQLStore) ListRuntimeEventSources(ctx context.Context) ([]model.RuntimeEventSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,runtime_id,cluster_id,node_id,engine_id,state,first_seen_at,last_heartbeat_at,last_error FROM runtime_event_sources ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []model.RuntimeEventSource{}
	for rows.Next() {
		var v model.RuntimeEventSource
		if err := rows.Scan(&v.ID, &v.RuntimeID, &v.ClusterID, &v.NodeID, &v.EngineID, &v.State, &v.FirstSeenAt, &v.LastHeartbeatAt, &v.LastError); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (s *SQLStore) syncTimelineScopes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,parent_id,payload FROM control_documents WHERE kind='environment'`)
	if err != nil {
		return err
	}
	type scope struct{ project, environment string }
	scopes := map[string]scope{}
	for rows.Next() {
		var id, project, raw string
		if err := rows.Scan(&id, &project, &raw); err != nil {
			rows.Close()
			return err
		}
		var value struct {
			ApplicationID string `json:"application_id"`
		}
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			rows.Close()
			return err
		}
		if value.ApplicationID != "" {
			scopes[value.ApplicationID] = scope{project, id}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT op.id,
		COALESCE(NULLIF(op.application_id,''),(SELECT application_id FROM services WHERE id=op.service_id),'') AS linked_application,
		op.project_id,op.environment_id FROM deployment_operations op WHERE op.scope_snapshot=FALSE`)
	if err != nil {
		return err
	}
	type correction struct{ id, project, environment string }
	updates := []correction{}
	for rows.Next() {
		var id, applicationID, projectID, environmentID string
		if err := rows.Scan(&id, &applicationID, &projectID, &environmentID); err != nil {
			rows.Close()
			return err
		}
		want := scopes[applicationID]
		if projectID != want.project || environmentID != want.environment {
			updates = append(updates, correction{id, want.project, want.environment})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := s.db.ExecContext(ctx, s.bind(`UPDATE deployment_operations SET project_id=:project_id,environment_id=:environment_id WHERE id=:id AND scope_snapshot=FALSE`),
			update.project, update.environment, update.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLStore) ListTimeline(ctx context.Context, filter TimelineFilter, page Page) ([]model.TimelineRecord, error) {
	if err := s.syncTimelineScopes(ctx); err != nil {
		return nil, err
	}
	page = page.normalized()
	query := `SELECT source,id,source_id,runtime_id,service_id,application_id,project_id,environment_id,node_id,instance_id,actor_type,actor_id,action,category,status,exit_code,oom_killed,occurred_at,observed_at,gap_end_at FROM (
		SELECT 'operation' AS source,id,'' AS source_id,runtime_id,service_id,application_id,project_id,environment_id,'' AS node_id,'' AS instance_id,'operation' AS actor_type,'' AS actor_id,operation_type AS action,'planned' AS category,status,NULL AS exit_code,FALSE AS oom_killed,created_at AS occurred_at,updated_at AS observed_at,NULL AS gap_end_at FROM deployment_operations
		UNION ALL
		SELECT 'docker' AS source,id,source_id,runtime_id,service_id,application_id,project_id,environment_id,node_id,instance_id,actor_type,actor_id,action,category,status,exit_code,oom_killed,occurred_at,observed_at,gap_end_at FROM runtime_events
	) timeline WHERE 1=1`
	args := []any{}
	gapRuntimes := filter.GapRuntimeIDs
	if gapRuntimes == nil && filter.RuntimeID != "" {
		gapRuntimes = []string{filter.RuntimeID}
	}
	appendGapMatch := func() string {
		ids := namedPlaceholders("gap_runtime_id", len(gapRuntimes))
		for _, runtimeID := range gapRuntimes {
			args = append(args, runtimeID)
		}
		for _, runtimeID := range gapRuntimes {
			args = append(args, runtimeID)
		}
		return `(category='gap' AND (runtime_id IN (` + ids + `) OR source_id IN (
			SELECT source.id FROM runtime_event_sources source JOIN runtimes rt ON rt.cluster_id=source.cluster_id
			WHERE source.cluster_id<>'' AND rt.id IN (` + ids + `))))`
	}
	for _, item := range []struct{ column, value string }{{"project_id", filter.ProjectID}, {"environment_id", filter.EnvironmentID}, {"runtime_id", filter.RuntimeID}, {"application_id", filter.ApplicationID}, {"service_id", filter.ServiceID}, {"category", filter.Category}} {
		if item.value != "" {
			query += ` AND (` + item.column + `=:` + item.column
			args = append(args, item.value)
			if item.column != "category" && len(gapRuntimes) > 0 {
				query += ` OR ` + appendGapMatch()
			}
			query += `)`
		}
	}
	if !filter.BeforeAt.IsZero() {
		query += ` AND (occurred_at<:occurred_at OR (occurred_at=:occurred_at_2 AND id<:id))`
		args = append(args, filter.BeforeAt, filter.BeforeAt, filter.BeforeID)
	}
	query += ` ORDER BY occurred_at DESC,id DESC LIMIT :limit`
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []model.TimelineRecord{}
	for rows.Next() {
		var value model.TimelineRecord
		var exitCode sql.NullInt64
		var gapEnd sql.NullTime
		if err := rows.Scan(&value.Source, &value.ID, &value.SourceID, &value.RuntimeID, &value.ServiceID, &value.ApplicationID,
			&value.ProjectID, &value.EnvironmentID, &value.NodeID, &value.InstanceID, &value.ActorType,
			&value.ActorID, &value.Action, &value.Category, &value.Status, &exitCode, &value.OOMKilled,
			&value.OccurredAt, &value.ObservedAt, &gapEnd); err != nil {
			return nil, err
		}
		if exitCode.Valid {
			code := int(exitCode.Int64)
			value.ExitCode = &code
		}
		if gapEnd.Valid {
			value.GapEndAt = &gapEnd.Time
		}
		if value.Source == "operation" {
			value.OperationID = value.ID
		} else if value.Category == "gap" {
			value.Source = "gap"
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *SQLStore) PruneRuntimeEvents(ctx context.Context, before time.Time) error {
	_, err := s.db.ExecContext(ctx, s.bind(`DELETE FROM runtime_events WHERE occurred_at<:occurred_at`), before)
	return err
}
