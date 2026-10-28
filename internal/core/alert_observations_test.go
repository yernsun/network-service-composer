package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestAlertOwnershipUsesDurableBindingsAndPreservesSharedScopes(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := t.Context()
	project, err := c.CreateProject(ctx, "Owned", "owned")
	if err != nil {
		t.Fatal(err)
	}
	first, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "dev", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CreateEnvironment(ctx, project.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	first.ApplicationID, first.GatewayIDs = "app-first", []string{"gateway-shared"}
	second.ApplicationID, second.GatewayIDs = "app-second", []string{"gateway-shared"}
	for _, env := range []Environment{first, second} {
		if err := c.saveControl(ctx, env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		kind, id string
		count    int
	}{
		{"runtime", rt.ID, 2}, {"gateway", "gateway-shared", 2}, {"application", "app-first", 1},
		{"environment", second.ID, 1}, {"application", "deleted-id", 0}, {"unscoped", "anything", 0},
	} {
		got, err := c.alertScopes(ctx, tc.kind, tc.id)
		if err != nil || len(got) != tc.count {
			t.Fatal(tc, got, err)
		}
		for _, scope := range got {
			if scope.ProjectID != project.ID || !slices.Contains([]string{first.ID, second.ID}, scope.EnvironmentID) {
				t.Fatal("invented ownership", scope)
			}
		}
	}
	for _, event := range []NotificationEvent{
		{Type: "image.available", EnvironmentID: second.ID},
		{Type: "deployment.failed", ResourceID: first.ApplicationID, EnvironmentID: "forged"},
		{Type: "runtime.problem", ResourceID: rt.ID},
		{Type: "gateway.offline", ResourceID: "gateway-shared"},
		{Type: "notification.test"}, {Type: "unrecognized"},
	} {
		original := event.Type
		if err := c.notificationScopes(ctx, &event); err != nil {
			t.Fatal(err)
		}
		if original == "deployment.failed" && (event.EnvironmentID != first.ID || len(event.Scopes) != 1) {
			t.Fatal("caller supplied ownership survived", event)
		}
		if original == "runtime.problem" && (event.EnvironmentID != "" || len(event.Scopes) != 2) {
			t.Fatal("shared resource narrowed", event)
		}
	}
}

type alertTargetStore struct {
	storage.Store
	apps       []model.Application
	runtime    model.Runtime
	presence   storage.RuntimePresence
	services   []model.Service
	revision   model.ApplicationRevision
	operations []model.DeploymentOperation
	boundary   string
	busy       bool
	err        error
}

func (s alertTargetStore) GetApplicationRevision(ctx context.Context, id string) (model.ApplicationRevision, error) {
	if s.boundary == "revision" {
		return model.ApplicationRevision{}, errAlertOwnership
	}
	if s.revision.ID != "" {
		return s.revision, nil
	}
	return s.Store.GetApplicationRevision(ctx, id)
}

func targetIntentRevision(t *testing.T, c *Core, app model.Application) model.ApplicationRevision {
	t.Helper()
	raw := []byte("services:\n  web:\n    image: nginx:alpine\n")
	encrypted, err := c.keyring.Encrypt(raw, revisionAAD(app.ActiveRevisionID, "resolved"))
	if err != nil {
		t.Fatal(err)
	}
	return model.ApplicationRevision{ID: app.ActiveRevisionID, ApplicationID: app.ID, EncryptedResolved: encrypted, EncryptionKeyID: c.keyring.PrimaryID(), ResolvedSHA256: digestString(string(raw))}
}

func (s alertTargetStore) ListApplications(_ context.Context, _ string, p storage.Page) ([]model.Application, error) {
	if s.err != nil {
		return nil, s.err
	}
	start := 0
	for start < len(s.apps) && s.apps[start].ID <= p.After {
		start++
	}
	return s.apps[start:min(start+p.Limit, len(s.apps))], nil
}
func (s alertTargetStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	if s.boundary == "runtime" {
		return model.Runtime{}, errAlertOwnership
	}
	return s.runtime, nil
}
func (s alertTargetStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if key == storage.RuntimePresenceID(s.runtime.ID) {
		if s.boundary == "presence" {
			return storage.ControlDocument{}, errAlertOwnership
		}
		if s.boundary == "missing presence" {
			return storage.ControlDocument{}, storage.ErrNotFound
		}
		raw, err := json.Marshal(s.presence)
		return storage.ControlDocument{ID: key, Kind: "runtime-presence", Payload: raw}, err
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s alertTargetStore) ListAllServicesFiltered(context.Context, storage.ServiceFilter) ([]model.Service, error) {
	if s.boundary == "services" {
		return nil, errAlertOwnership
	}
	return s.services, nil
}
func (s alertTargetStore) ListDeploymentOperations(_ context.Context, _ string, page storage.Page) ([]model.DeploymentOperation, error) {
	if s.boundary == "history" {
		return nil, errAlertOwnership
	}
	if s.busy {
		return []model.DeploymentOperation{{Status: model.OperationRunning}}, nil
	}
	start := 0
	for start < len(s.operations) && s.operations[start].ID <= page.After {
		start++
	}
	return s.operations[start:min(start+page.Limit, len(s.operations))], nil
}

func TestTargetLossRequiresCompleteFreshInventoryAndRetainsUnknownHistory(t *testing.T) {
	for _, scenario := range []string{"missing", "present", "zero replicas", "observed zero", "unreadable intent", "foreign revision", "deleted service", "managed service head", "unmanaged service", "stopped", "down", "unmanaged", "system", "pending revision", "no baseline", "runtime disabled", "zero epoch", "foreign epoch", "missing presence", "offline", "stale", "incomplete epoch", "foreign presence", "old presence", "empty inventory", "pending operation"} {
		t.Run(scenario, func(t *testing.T) {
			c, rt, _ := controlFixture(t)
			now := c.now()
			c.now = func() time.Time { return now }
			rt.LastSeenAt = &now
			rt.ObservationEpoch = 1
			rt.AppliedObservation = 1
			app := model.Application{ID: "app-target", ProjectName: "target", RuntimeID: rt.ID, Managed: true, Status: model.ApplicationRunning, ActiveRevisionID: "revision", DesiredRevisionID: "revision"}
			s := alertTargetStore{Store: c.store, apps: []model.Application{app}, runtime: rt, revision: targetIntentRevision(t, c, app), presence: storage.RuntimePresence{RuntimeID: rt.ID, Observation: 1, ObservedAt: now}, services: []model.Service{{ID: "service-target", ServiceKey: "target_web", Managed: true, DesiredInstances: 1}}}
			switch scenario {
			case "present":
				s.presence.ServiceIDs = []string{"service-target"}
			case "zero replicas":
				s.apps[0].ReplicaOverrides = map[string]int{"web": 0}
			case "observed zero":
				s.services[0].DesiredInstances = 0
			case "unreadable intent":
				s.revision.EncryptedResolved = []byte("invalid envelope")
			case "foreign revision":
				s.revision.ApplicationID = "foreign"
			case "deleted service":
				s.revision.ConfigurationSource.ServiceRevisions = map[string]string{}
			case "managed service head":
				s.revision.ConfigurationSource.ServiceRevisions = map[string]string{"web": "revision"}
			case "unmanaged service":
				s.services[0].Managed = false
			case "stopped":
				s.apps[0].Status = model.ApplicationStopped
			case "down":
				s.apps[0].Status = model.ApplicationDown
			case "unmanaged":
				s.apps[0].Managed = false
			case "system":
				s.apps[0].System = true
			case "pending revision":
				s.apps[0].DesiredRevisionID = "new-revision"
			case "no baseline":
				s.apps[0].ActiveRevisionID = ""
			case "runtime disabled":
				s.runtime.Enabled = false
			case "zero epoch":
				s.presence.Observation = 0
			case "foreign epoch":
				s.presence.Observation++
			case "missing presence":
				s.boundary = "missing presence"
			case "offline":
				s.runtime.Status = model.RuntimeOffline
			case "stale":
				old := now.Add(-time.Hour)
				s.runtime.LastSeenAt = &old
			case "incomplete epoch":
				s.runtime.ObservationEpoch++
			case "foreign presence":
				s.presence.RuntimeID = "another-runtime"
			case "old presence":
				s.presence.ObservedAt = now.Add(-time.Second)
			case "empty inventory":
				s.services = nil
			case "pending operation":
				s.busy = true
			}
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); err != nil {
				t.Fatal(err)
			}
			issues, err := c.Issues(t.Context(), storage.Page{Limit: 200})
			if err != nil || (len(issues) == 1) != (scenario == "missing" || scenario == "observed zero" || scenario == "managed service head") {
				t.Fatal("incorrect target loss", issues, err)
			}
			if scenario != "missing" {
				return
			}
			first := issues[0]
			ack, err := c.AcknowledgeIssue(t.Context(), first.ID, first.Revision, "user-owner")
			if err != nil || ack.AcknowledgedBy != "user-owner" || ack.AcknowledgedAt == nil {
				t.Fatal(ack, err)
			}
			s.runtime.ObservationEpoch++
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); err != nil {
				t.Fatal(err)
			}
			issues, _ = c.Issues(t.Context(), storage.Page{Limit: 200})
			if !issues[0].Active || issues[0].ResolvedAt != nil || !issues[0].Acknowledged || issues[0].ObservationState != "UNKNOWN" {
				t.Fatal("unknown resolved or acknowledged issue", issues)
			}
			s.runtime.ObservationEpoch--
			s.presence.ServiceIDs = []string{"service-target"}
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); err != nil {
				t.Fatal(err)
			}
			issues, _ = c.Issues(t.Context(), storage.Page{Limit: 200})
			if issues[0].Active || issues[0].ResolvedAt == nil {
				t.Fatal("fresh restoration did not resolve", issues)
			}
			now = now.Add(time.Second)
			s.runtime.LastSeenAt = &now
			s.presence.ObservedAt = now
			s.presence.ServiceIDs = nil
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); err != nil {
				t.Fatal(err)
			}
			issues, _ = c.Issues(t.Context(), storage.Page{Limit: 200})
			if !issues[0].Active || issues[0].Acknowledged || issues[0].AcknowledgedAt != nil || issues[0].Occurrences != 2 || issues[0].OccurrenceStartedAt == nil || !issues[0].OccurrenceStartedAt.Equal(now) {
				t.Fatal("recurrence lost identity", issues)
			}
		})
	}
}

func TestAlertTargetPaginationAndSourceFailure(t *testing.T) {
	c, rt, _ := controlFixture(t)
	s := alertTargetStore{Store: c.store, runtime: rt}
	for i := 0; i < 201; i++ {
		s.apps = append(s.apps, model.Application{ID: fmt.Sprintf("app-%04d", i), Managed: true, Status: model.ApplicationStopped})
	}
	c.store = s
	if err := c.refreshTargetIssues(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.err = errors.New("inventory unavailable")
	c.store = s
	if err := c.refreshTargetIssues(t.Context()); !errors.Is(err, s.err) {
		t.Fatal("inventory failure hidden", err)
	}
}

func TestCertificatePendingCannotResolveKnownRisk(t *testing.T) {
	c, _, pool := controlFixture(t)
	ctx := t.Context()
	now := c.now()
	c.now = func() time.Time { return now }
	cert := model.Certificate{ID: "cert-risk", PoolID: pool.ID, Provider: "custom", Status: model.CertificateRevoked, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
	if err := c.store.CreateCertificate(ctx, &cert); err != nil {
		t.Fatal(err)
	}
	if err := c.RefreshCommunityIssues(ctx); err != nil {
		t.Fatal(err)
	}
	for _, status := range []model.CertificateStatus{model.CertificatePending, model.CertificateActive, model.CertificateIssued} {
		cert.Status = status
		if err := c.store.UpdateCertificate(ctx, &cert, cert.Revision); err != nil {
			t.Fatal(err)
		}
		cert.Revision++
		if err := c.RefreshCommunityIssues(ctx); err != nil {
			t.Fatal(err)
		}
		issues, err := c.Issues(ctx, storage.Page{Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, issue := range issues {
			if issue.ResourceID == cert.ID {
				found = true
				if !issue.Active || issue.ResolvedAt != nil || issue.ObservationState != "UNKNOWN" {
					t.Fatal("unconfirmed certificate falsely recovered", status, issue)
				}
			}
		}
		if !found {
			t.Fatal("certificate issue disappeared")
		}
	}
}

func TestTargetNotificationsRequireFreshRecoveryAndStayLinked(t *testing.T) {
	c, env := imageEnvironmentFixture(t)
	ctx := t.Context()
	now := c.now()
	c.now = func() time.Time { return now }
	app, err := c.store.GetApplication(ctx, env.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := c.store.GetRuntime(ctx, env.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	rt.LastSeenAt = &now
	rt.ObservationEpoch = 1
	rt.AppliedObservation = 1
	s := alertTargetStore{Store: c.store, apps: []model.Application{app}, runtime: rt, presence: storage.RuntimePresence{RuntimeID: rt.ID, Observation: 1, ObservedAt: now}, services: []model.Service{{ID: "target", ServiceKey: app.ProjectName + "/web", Managed: true, DesiredInstances: 1}}}
	c.store = s
	endpoint, secret := "https://receiver.example.test/hook", "private-test-signing-secret"
	settings, err := c.SaveNotificationSettings(ctx, NotificationSettingsInput{Enabled: true, Events: []string{"deployments"}, Endpoint: &endpoint, Secret: &secret}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := c.targetNotifications(ctx, now, settings.Revision); err != nil {
			t.Fatal(err)
		}
	}
	rows := notices(t, c)
	if len(rows) != 1 || rows[0].Event.Type != "deployment.target_lost" || rows[0].Event.IssueID == "" || rows[0].Event.Occurrence != 1 || rows[0].Event.EnvironmentID != env.ID {
		t.Fatal("target incident not scoped or duplicated", rows)
	}
	issueID := rows[0].Event.IssueID
	s.runtime.ObservationEpoch++
	c.store = s
	if err := c.targetNotifications(ctx, now, settings.Revision); err != nil || len(notices(t, c)) != 1 {
		t.Fatal("partial inventory sent recovery", err)
	}
	s.runtime.ObservationEpoch--
	s.presence.ServiceIDs = []string{"target"}
	c.store = s
	if err := c.targetNotifications(ctx, now, settings.Revision); err != nil {
		t.Fatal(err)
	}
	rows = notices(t, c)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	for _, row := range rows {
		if row.Event.IssueID != issueID || row.Event.Occurrence != 1 {
			t.Fatal("recovery lost incident linkage", row)
		}
	}
}

func TestTargetLossSourceFailuresPreserveTheKnownIncident(t *testing.T) {
	for _, boundary := range []string{"runtime", "presence", "services", "revision", "history"} {
		t.Run(boundary, func(t *testing.T) {
			c, rt, _ := controlFixture(t)
			now := c.now()
			rt.LastSeenAt = &now
			rt.ObservationEpoch = 1
			rt.AppliedObservation = 1
			app := model.Application{ID: "app", ProjectName: "app", RuntimeID: rt.ID, Managed: true, Status: model.ApplicationRunning, ActiveRevisionID: "revision", DesiredRevisionID: "revision"}
			s := alertTargetStore{Store: c.store, apps: []model.Application{app}, runtime: rt, revision: targetIntentRevision(t, c, app), presence: storage.RuntimePresence{RuntimeID: rt.ID, Observation: 1, ObservedAt: now}, services: []model.Service{{ID: "web", ServiceKey: "app/web", Managed: true}}}
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := c.Issues(t.Context(), storage.Page{})
			if err != nil || len(before) != 1 {
				t.Fatal(before, err)
			}
			s.boundary = boundary
			c.store = s
			if err := c.refreshTargetIssues(t.Context()); !errors.Is(err, errAlertOwnership) {
				t.Fatal("collection failure hidden", err)
			}
			after, err := c.Issues(t.Context(), storage.Page{})
			if err != nil || len(after) != 1 || !after[0].Active || after[0].ResolvedAt != nil || after[0].Revision != before[0].Revision {
				t.Fatal("source failure changed the known incident", after, err)
			}
		})
	}
}

func TestTargetLossLinksTheLatestSuccessfulBaselineOperation(t *testing.T) {
	c, rt, _ := controlFixture(t)
	now := c.now()
	app := model.Application{ID: "app", ActiveRevisionID: "revision"}
	s := alertTargetStore{Store: c.store, runtime: rt}
	for i := 0; i < 201; i++ {
		op := model.DeploymentOperation{ID: fmt.Sprintf("op-%04d", i), RevisionID: "revision", Status: model.OperationFailed}
		if i < 4 {
			op.Status = model.OperationSucceeded
			op.CreatedAt = now.Add(time.Duration([]int{0, 2, 1, 3}[i]) * time.Second)
		}
		if i == 3 {
			op.RevisionID = "foreign-revision"
		}
		s.operations = append(s.operations, op)
	}
	c.store = s
	busy, id, err := c.alertApplicationOperation(t.Context(), app)
	if err != nil || busy || id != "op-0001" {
		t.Fatal("wrong baseline operation", busy, id, err)
	}
	for _, status := range []model.DeploymentOperationStatus{model.OperationQueued, model.OperationRunning} {
		s.operations[0].Status = status
		c.store = s
		busy, id, err = c.alertApplicationOperation(t.Context(), app)
		if err != nil || !busy || id != "op-0000" {
			t.Fatal("active work was not retained", busy, id, err)
		}
	}
}

func TestRuntimeIssuesContinuePastDisabledPagesWithoutNotifications(t *testing.T) {
	c, rt, _ := controlFixture(t)
	rt.Status = model.RuntimeOffline
	pages := &runtimeNoticePages{Store: c.store, runtime: rt}
	c.store = pages
	if err := c.refreshRuntimeIssues(t.Context()); err != nil {
		t.Fatal(err)
	}
	issues, err := c.Issues(t.Context(), storage.Page{})
	if err != nil || len(issues) != 1 || issues[0].ResourceID != rt.ID || len(pages.calls) != 2 || len(notices(t, c)) != 0 {
		t.Fatal("issues depended on outbound receiver configuration", issues, pages.calls, err)
	}
}

func TestUnknownObservationRetainsRelatedOperation(t *testing.T) {
	c, _, _ := controlFixture(t)
	bad := true
	if err := c.observeIssueState(t.Context(), "deployment_target_lost", "application", "app", "/", &bad, "CONFIRMED", "operation-original"); err != nil {
		t.Fatal(err)
	}
	if err := c.observeIssueState(t.Context(), "deployment_target_lost", "application", "app", "/", nil, "UNKNOWN", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := c.Issues(t.Context(), storage.Page{})
	if err != nil || len(rows) != 1 || rows[0].OperationID != "operation-original" || !rows[0].Active || rows[0].ObservationState != "UNKNOWN" {
		t.Fatal("unknown observation lost immutable context", rows, err)
	}
}
