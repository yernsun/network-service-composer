package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/hoststats"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/nodecontrol"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func dependencyEnvironment(t *testing.T) (*Core, Environment, model.Certificate, model.Domain) {
	t.Helper()
	c, env := imageEnvironmentFixture(t)
	now := c.now()
	c.now = func() time.Time { return now }
	rt, err := c.store.GetRuntime(t.Context(), env.RuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	metadata := model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}
	before, after := now.Add(-time.Hour), now.Add(90*24*time.Hour)
	cert := model.Certificate{ID: "dependency-certificate", PoolID: rt.PoolID, Provider: "acme", Status: model.CertificateActive, NotBefore: &before, NotAfter: &after, Metadata: metadata}
	if err = c.store.CreateCertificate(t.Context(), &cert); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: "dependency-domain", PoolID: rt.PoolID, Host: "dependencies.example.test", TLSMode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "fixture", CertificateID: cert.ID, Source: model.SourceAPI, Enabled: true, Metadata: metadata}
	if err = c.store.CreateDomain(t.Context(), &domain); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: "dependency-service", RuntimeID: rt.ID, ApplicationID: env.ApplicationID, ServiceKey: "dependency/web", Name: "web", Metadata: metadata}
	if err = c.store.UpsertService(t.Context(), &service); err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: "dependency-route", DomainID: domain.ID, ServiceID: service.ID, Kind: "proxy", Match: "prefix", Path: "/", UpstreamProtocol: "http", Enabled: true, Source: model.SourceAPI, Metadata: metadata}
	if err = c.store.CreateRoute(t.Context(), &route); err != nil {
		t.Fatal(err)
	}
	c.config.ACME.AcceptTerms = true
	c.config.ACME.Email = "operator@example.test"
	c.config.ACME.DNSProviders = map[string]config.DNSProviderProfile{"fixture": {Provider: "he", Credentials: config.DNSProviderCredentials{}}}
	return c, env, cert, domain
}

func TestEnvironmentDependenciesSeparateConfigurationCurrentEvidenceAndHistoricalSuccess(t *testing.T) {
	c, env, cert, _ := dependencyEnvironment(t)
	value, err := c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "AVAILABLE" || value.Certificates.ObservedAt == nil || value.Certificates.LastSucceededAt == nil || value.ACME.State != "CONFIGURED" || value.Notifications.State != "BLOCKED" || value.HA.State != "UNKNOWN" {
		t.Fatal(value, err)
	}
	receiver := routedReceiver(t, c, "receiver_environment")
	routedRule(t, c, "environment", env.ID, receiver.ID)
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Notifications.State != "CONFIGURED" {
		t.Fatal(value.Notifications, err)
	}
	at := c.now().Add(-time.Hour)
	row := Notification{Event: NotificationEvent{ID: "historic", Type: "runtime.problem", Scopes: []AlertScope{{ProjectID: env.ProjectID, EnvironmentID: env.ID}}}, Status: "DELIVERED", DeliveredAt: &at, ReceiverID: receiver.ID}
	if err = c.saveControl(t.Context(), "notice_historic", notificationDeliveryKind, "", row, 0); err != nil {
		t.Fatal(err)
	}
	receiver.Enabled = false
	if _, err = c.SaveNotificationReceiver(t.Context(), receiver.ID, NotificationReceiverInput{Name: receiver.Name, Enabled: false}, receiver.Revision); err != nil {
		t.Fatal(err)
	}
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Notifications.State != "HISTORICAL" || value.Notifications.LastSucceededAt == nil {
		t.Fatal(value.Notifications, err)
	}
	raw, _ := json.Marshal(value)
	for _, private := range []string{"receiver_environment", "example.test/hook", "private-signing", "credentials_file"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("environment prerequisite projection leaked receiver/provider configuration", private)
		}
	}
	cert.Status = model.CertificateRenewing
	if err = c.store.UpdateCertificate(t.Context(), &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "UNKNOWN" {
		t.Fatal("renewal progress became current success", value.Certificates, err)
	}
}

func TestEnvironmentCertificateRiskAndMissingProfileRemainVisibleWithoutImageBaseline(t *testing.T) {
	c, env, cert, _ := dependencyEnvironment(t)
	future := c.now().Add(time.Hour)
	cert.NotBefore = &future
	if err := c.store.UpdateCertificate(t.Context(), &cert, cert.Revision); err != nil {
		t.Fatal(err)
	}
	delete(c.config.ACME.DNSProviders, "fixture")
	value, err := c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "BLOCKED" || value.ACME.State != "BLOCKED" {
		t.Fatal(value, err)
	}
	c.config.Management.Enabled = false
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.ImageUpdates.State != "BLOCKED" || value.Certificates.State != "BLOCKED" || value.ACME.State != "BLOCKED" {
		t.Fatal("management restriction hid independent certificate prerequisites", value, err)
	}
	env.ApplicationID = ""
	if err = c.saveControl(t.Context(), env.ID, "environment", env.ProjectID, env, env.Revision); err != nil {
		t.Fatal(err)
	}
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "UNKNOWN" {
		t.Fatal("shared Runtime invented TLS ownership", value, err)
	}
	// No mutable task or source is created by any capability read.
	rows, err := c.store.ListDeploymentOperations(t.Context(), "", storage.Page{Limit: 200})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}

func TestEnvironmentHARequiresLiveControllerAndExecutorWithoutClaimingExternalFailover(t *testing.T) {
	c, rt, _ := controlFixture(t)
	var err error
	rt.Type = model.RuntimeDocker
	rt.Endpoint = "node://capability-node"
	rt.ManagementEnabled = true
	if err = c.store.UpdateRuntime(t.Context(), &rt, rt.Revision); err != nil {
		t.Fatal(err)
	}
	project, err := c.CreateProject(t.Context(), "Prerequisites", "prerequisites")
	if err != nil {
		t.Fatal(err)
	}
	env, err := c.CreateEnvironment(t.Context(), project.ID, EnvironmentInput{Name: "prod", RuntimeID: rt.ID})
	if err != nil {
		t.Fatal(err)
	}
	c.config.Controller.Nodes = []config.ControllerNodeConfig{{InstanceID: "capability-controller", NodeID: "capability-node", EngineID: "capability-engine", ClusterID: "capability-cluster"}}
	now := c.now()
	live := true
	reports := &nodecontrol.Reports{Store: c.store, Keys: c.keyring, Bindings: map[string]nodecontrol.Binding{"capability-controller": {InstanceID: "capability-controller", NodeID: "capability-node", EngineID: "capability-engine", ClusterID: "capability-cluster"}}, Now: func(context.Context) (time.Time, error) { return now, nil }, CheckOwner: func(context.Context, string, int64) error {
		if !live {
			return errors.New("lost collector")
		}
		return nil
	}}
	session, err := reports.Begin(t.Context(), "capability-controller", "capability-node", "capability-engine", "boot", 11)
	if err != nil {
		t.Fatal(err)
	}
	if err = reports.Accept(t.Context(), "capability-controller", nodecontrol.Report{NodeID: "capability-node", EngineID: "capability-engine", Generation: session.Generation, Sequence: 1, Observation: nodecontrol.Observation{At: now, Complete: true, Info: system.Info{ID: "capability-engine", Swarm: swarm.Info{NodeID: "capability-node", Cluster: &swarm.ClusterInfo{ID: "capability-cluster"}}}, Health: &hoststats.Health{InstanceID: "capability-controller", Execution: "enabled"}}}); err != nil {
		t.Fatal(err)
	}
	c.HAController = func(context.Context) HAEvidence { return HAEvidence{State: "READY"} }
	c.HAReports = reports
	value, err := c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.HA.State != "CONFIGURED" || value.HA.ObservedAt == nil || value.HA.Reason != "ha_external_dependencies_unverified" {
		t.Fatal(value.HA, err)
	}
	live = false
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.HA.State != "UNKNOWN" {
		t.Fatal("lost executor lease retained readiness", value.HA, err)
	}
	live = true
	c.HAController = func(context.Context) HAEvidence { return HAEvidence{State: "UNKNOWN"} }
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.HA.State != "UNKNOWN" {
		t.Fatal(value.HA, err)
	}
	c.HAReports = nil
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.HA.State != "UNKNOWN" {
		t.Fatal(value.HA, err)
	}
	for _, field := range []string{"terms", "email", "directory"} {
		c.config.ACME.AcceptTerms = true
		c.config.ACME.Email = "operator@example.test"
		c.config.ACME.DirectoryURL = "https://acme.example.test/directory"
		switch field {
		case "terms":
			c.config.ACME.AcceptTerms = false
		case "email":
			c.config.ACME.Email = ""
		case "directory":
			c.config.ACME.DirectoryURL = ""
		}
		value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
		if err != nil || value.ACME.State != "BLOCKED" {
			t.Fatal(field, value.ACME, err)
		}
	}
}

type dependencyRouteInventory struct {
	storage.Store
	routes  []model.Route
	failure string
}

func (s dependencyRouteInventory) ListRoutes(_ context.Context, p storage.Page) ([]model.Route, error) {
	if s.failure == "later-page" && p.After != "" {
		return nil, errors.New("inventory interrupted")
	}
	rows := []model.Route{}
	for _, row := range s.routes {
		if row.ID > p.After && len(rows) < p.Limit {
			rows = append(rows, row)
		}
	}
	return rows, nil
}
func (s dependencyRouteInventory) GetService(ctx context.Context, id string) (model.Service, error) {
	if s.failure == "service-removed" {
		return model.Service{}, storage.ErrNotFound
	}
	if id == "foreign-service" {
		return model.Service{ApplicationID: "other-environment-application"}, nil
	}
	return s.Store.GetService(ctx, id)
}
func (s dependencyRouteInventory) GetDomain(ctx context.Context, id string) (model.Domain, error) {
	if s.failure == "domain-removed" {
		return model.Domain{}, storage.ErrNotFound
	}
	if id == "plain-domain" {
		return model.Domain{ID: id, TLSMode: model.TLSDisabled}, nil
	}
	return s.Store.GetDomain(ctx, id)
}
func (s dependencyRouteInventory) GetCertificate(ctx context.Context, id string) (model.Certificate, error) {
	if s.failure == "certificate-removed" {
		return model.Certificate{}, storage.ErrNotFound
	}
	return s.Store.GetCertificate(ctx, id)
}
func TestEnvironmentTLSOwnershipSurvivesPaginationDuplicatesAndConcurrentRemoval(t *testing.T) {
	c, env, _, domain := dependencyEnvironment(t)
	inventory := dependencyRouteInventory{Store: c.store}
	inventory.routes = []model.Route{{ID: "route-000", DomainID: domain.ID}, {ID: "route-001", ServiceID: "foreign-service", DomainID: "unrelated-domain"}, {ID: "route-002", ServiceID: "dependency-service", DomainID: "plain-domain"}}
	for i := 3; i < 205; i++ {
		inventory.routes = append(inventory.routes, model.Route{ID: fmt.Sprintf("route-%03d", i), ServiceID: "dependency-service", DomainID: domain.ID})
	}
	c.store = inventory
	value, err := c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "AVAILABLE" {
		t.Fatal(value.Certificates, err)
	}
	for _, mode := range []string{"later-page", "service-removed", "domain-removed"} {
		inventory.failure = mode
		c.store = inventory
		if _, err = c.GetEnvironmentCapabilities(t.Context(), env.ID); err == nil {
			t.Fatal("partial ownership inventory became current certificate evidence", mode)
		}
	}
	inventory.failure = "certificate-removed"
	c.store = inventory
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "UNKNOWN" {
		t.Fatal("removed certificate became current", value.Certificates, err)
	}
	inventory.failure = ""
	inventory.routes = []model.Route{{ID: "route", ServiceID: "dependency-service", DomainID: "plain-domain"}}
	c.store = inventory
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "UNKNOWN" {
		t.Fatal("plaintext route acquired TLS readiness", value.Certificates, err)
	}
	domain.CertificateID = ""
	if err = inventory.Store.UpdateDomain(t.Context(), &domain, domain.Revision); err != nil {
		t.Fatal(err)
	}
	inventory.routes = []model.Route{{ID: "route", ServiceID: "dependency-service", DomainID: domain.ID}}
	c.store = inventory
	value, err = c.GetEnvironmentCapabilities(t.Context(), env.ID)
	if err != nil || value.Certificates.State != "BLOCKED" || value.Certificates.Reason != "certificate_pending" {
		t.Fatal(value.Certificates, err)
	}
}
