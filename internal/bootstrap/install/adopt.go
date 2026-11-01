package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type adoptionEvidence struct {
	RegisteredState         bootstrap.State        `json:"registered_state"`
	RegisteredConfigDigest  string                 `json:"registered_config_digest,omitempty"`
	Installation            bootstrap.Installation `json:"installation"`
	IdentityDigest          string                 `json:"identity_digest"`
	RegistrationRevision    int64                  `json:"registration_revision"`
	ControllerConfiguration string                 `json:"controller_configuration"`
}

// adoptionView builds an isolated candidate. Planning never changes resource
// ownership, existing runtime configuration, credentials or administrator rows.
func (i *Installer) adoptionView(ctx context.Context, o Observation) (*Installer, adoptionEvidence, error) {
	a := i.Config.Adoption
	if a == nil {
		return nil, adoptionEvidence{}, fmt.Errorf("adoption requires explicit pre-existing registration and storage references")
	}
	cfg, err := i.localConfig(ctx)
	if err != nil {
		return nil, adoptionEvidence{}, err
	}
	registration := cfg.Controller.SystemProject
	if cfg.Storage.Driver != "postgres" || registration.ClusterID != a.ClusterID || registration.RuntimeID == "" || o.Nodes[i.Config.MaintenanceNode].Swarm.Cluster == nil || o.Nodes[i.Config.MaintenanceNode].Swarm.Cluster.ID != a.ClusterID {
		return nil, adoptionEvidence{}, fmt.Errorf("adoption cluster and registered Runtime do not agree")
	}
	record := *i.Record
	// Gateway domain TLS and the independent console listener are separate assets.
	record.CertificateID = ""
	record.ClusterID = a.ClusterID
	record.RuntimeID = registration.RuntimeID
	record.Topology = o.Topology
	record.Nodes = map[string]bootstrap.Identity{}
	record.Resources = map[string]bootstrap.Resource{}
	view := *i
	view.Record = &record
	registered := map[string]bool{}
	for _, s := range registration.Services {
		registered[s.ObjectID] = true
	}
	actual := map[string]swarm.Service{}
	for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
		id := a.ServiceIDs[key]
		if !registered[id] {
			return nil, adoptionEvidence{}, fmt.Errorf("adoption component is absent from the existing deployment registration")
		}
		service, err := i.inspectService(ctx, id)
		if err != nil {
			return nil, adoptionEvidence{}, err
		}
		if service.ID != id {
			return nil, adoptionEvidence{}, fmt.Errorf("adoption service identity differs")
		}
		if previous := i.Record.Resources["reserve:"+key]; previous.ID != "" && (previous.ID != service.ID || previous.Version > service.Version.Index || previous.SpecDigest != bootstrap.Digest(service.Spec)) {
			return nil, adoptionEvidence{}, fmt.Errorf("previously recorded component has changed")
		}
		actual[key] = service
		receipt := serviceReceipt(service)
		record.Resources["reserve:"+key] = receipt
		if key == "postgresql" {
			record.Resources["service:postgresql"] = receipt
		}
	}
	for _, node := range i.Config.Nodes {
		var binding config.ControllerNodeConfig
		for _, candidate := range cfg.Controller.Nodes {
			if candidate.NodeID == o.Nodes[node.Name].Swarm.NodeID {
				binding = candidate
			}
		}
		if binding.NodeID == "" && !node.Manager && o.Nodes[node.Name].Swarm.NodeID == a.DataNodeID {
			binding = config.ControllerNodeConfig{NodeID: a.DataNodeID, EngineID: o.Nodes[node.Name].ID, ClusterID: a.ClusterID}
		}
		if binding.NodeID == "" || binding.EngineID != o.Nodes[node.Name].ID || binding.ClusterID != a.ClusterID {
			return nil, adoptionEvidence{}, fmt.Errorf("node is not bound by the pre-existing authenticated Controller registration")
		}
		record.Nodes[node.Name] = bootstrap.Identity{InstanceID: binding.InstanceID, NodeID: binding.NodeID, EngineID: binding.EngineID, SocketGID: o.GIDs[node.Name]}
	}
	for _, node := range i.Config.Nodes {
		member, ok := o.Membership[record.Nodes[node.Name].NodeID]
		if !ok || (member.Spec.Role == swarm.NodeRoleManager) != node.Manager {
			return nil, adoptionEvidence{}, fmt.Errorf("registered Manager membership differs")
		}
		if (node.Name == i.Config.DataNode || node.Name == i.Config.MaintenanceNode) && (member.Status.State != swarm.NodeStateReady || member.Spec.Availability != swarm.NodeAvailabilityActive) {
			return nil, adoptionEvidence{}, fmt.Errorf("maintenance and data nodes must be Ready/Active")
		}
	}
	data := record.Nodes[i.Config.DataNode]
	if data.NodeID != a.DataNodeID {
		return nil, adoptionEvidence{}, fmt.Errorf("registered data node differs from adoption storage evidence")
	}
	database := actual["postgresql"]
	if database.Spec.Mode.Replicated == nil || database.Spec.Mode.Replicated.Replicas == nil || *database.Spec.Mode.Replicated.Replicas != 1 || database.Spec.TaskTemplate.Placement == nil || !contains(database.Spec.TaskTemplate.Placement.Constraints, "node.id == "+a.DataNodeID) {
		return nil, adoptionEvidence{}, fmt.Errorf("database must retain its single pinned task")
	}
	volumeFound := false
	for _, m := range database.Spec.TaskTemplate.ContainerSpec.Mounts {
		if m.Type == mount.TypeVolume && m.Source == a.Volume {
			volumeFound = true
		}
	}
	if !volumeFound {
		return nil, adoptionEvidence{}, fmt.Errorf("database volume does not match its explicit storage reference")
	}
	var volume struct{ Name, Driver string }
	if err = i.Hosts[i.Config.DataNode].Engine(ctx, "GET", "/volumes/"+url.PathEscape(a.Volume), nil, &volume); err != nil {
		return nil, adoptionEvidence{}, err
	}
	if volume.Name != a.Volume || volume.Driver != "local" {
		return nil, adoptionEvidence{}, fmt.Errorf("database volume identity or driver is unsupported")
	}
	record.Resources["volume:postgresql"] = bootstrap.Resource{Kind: "adopted-volume", ID: volume.Name, SpecDigest: bootstrap.Digest(data)}
	evidence := adoptionEvidence{ControllerConfiguration: bootstrap.Digest(cfg)}
	err = view.withStore(ctx, true, func(store storage.Store, _ config.Config) error {
		rt, err := store.GetRuntime(ctx, record.RuntimeID)
		if err != nil {
			return err
		}
		if rt.Type != model.RuntimeSwarm || rt.ClusterID != a.ClusterID || !rt.Enabled {
			return storage.ErrConflict
		}
		record.PoolID = rt.PoolID
		pool, err := store.GetPool(ctx, rt.PoolID)
		if err != nil {
			return err
		}
		if !pool.Enabled {
			return storage.ErrConflict
		}
		marker, err := store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
		if err == nil {
			var owner bootstrap.Installation
			if json.Unmarshal(marker.Payload, &owner) != nil || marker.Kind != bootstrap.InstallationDocumentKind || (a.InstallationID != "" && owner.ID != a.InstallationID) || owner.ID != record.ID || owner.MaintenanceAnchor != record.MaintenanceAnchor || owner.ClusterID != record.ClusterID || owner.RuntimeID != record.RuntimeID || owner.Database.NodeID != data.NodeID || owner.Database.EngineID != data.EngineID || owner.Database.Volume != a.Volume {
				return storage.ErrConflict
			}
			for key, service := range actual {
				if owner.Components[key].ID != service.ID {
					return storage.ErrConflict
				}
			}
			record.CertificateID = owner.CertificateID
			evidence.RegistrationRevision = marker.Revision
			evidence.RegisteredState = owner.State
			evidence.RegisteredConfigDigest = owner.ConfigDigest
		} else if !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		services, err := store.ListAllServicesFiltered(ctx, storage.ServiceFilter{RuntimeID: rt.ID})
		if err != nil {
			return err
		}
		for _, s := range services {
			selected := false
			for _, id := range a.ServiceIDs {
				selected = selected || s.RuntimeObjectID == id
			}
			if selected && (s.Managed || s.ApplicationID != "") {
				app, err := store.GetApplication(ctx, s.ApplicationID)
				if err != nil || !app.System || app.RuntimeID != rt.ID {
					return storage.ErrConflict
				}
			}
		}
		// These operations read/write system metadata only; no credential decryption.
		app := core.New(store, cfg, nil)
		gatewayProjectID := ""
		for page := (storage.Page{Limit: 200}); ; {
			projects, err := app.ListProjects(ctx, page)
			if err != nil {
				return err
			}
			for _, p := range projects {
				if strings.EqualFold(p.Name, "Gateway") || p.Slug == "gateway" {
					if !p.System {
						return storage.ErrConflict
					}
					gatewayProjectID = p.ID
				}
			}
			if len(projects) < page.Limit {
				break
			}
			page.After = projects[len(projects)-1].ID
		}
		gatewayMarker, markerErr := store.GetControlDocument(ctx, "system-project:gateway")
		if markerErr == nil {
			var registered struct {
				ProjectID string `json:"project_id"`
				ClusterID string `json:"cluster_id"`
				RuntimeID string `json:"runtime_id"`
			}
			if json.Unmarshal(gatewayMarker.Payload, &registered) != nil || gatewayMarker.Kind != "system-project" || registered.ProjectID != gatewayProjectID || registered.ClusterID != a.ClusterID || registered.RuntimeID != record.RuntimeID {
				return storage.ErrConflict
			}
		} else if !errors.Is(markerErr, storage.ErrNotFound) {
			return markerErr
		} else if gatewayProjectID != "" {
			return storage.ErrConflict
		}
		drift, err := view.adoptionGatewayIdentities(ctx, cfg, actual, o.Unavailable)
		if err != nil {
			return err
		}
		record.CertificateID, err = view.adoptedConsoleAsset(ctx, store, cfg, actual["controller"], record.CertificateID)
		if err != nil {
			return err
		}
		evidence.IdentityDigest = bootstrap.Digest(record.Nodes)
		evidence.Installation = view.seedRecord()
		evidence.Installation.UpdatedAt = time.Time{}
		evidence.Installation.CertificateID = record.CertificateID
		evidence.Installation.IngressNetwork = pool.BackendNetwork
		evidence.Installation.Database.Image = database.Spec.TaskTemplate.ContainerSpec.Image
		evidence.Installation.Drift = append([]string{"adoption_health_not_verified"}, drift...)
		if len(cfg.Controller.SystemProject.Services) > 4 {
			evidence.Installation.Drift = append(evidence.Installation.Drift, "additional_system_objects_require_cli_preparation")
		}
		if cfg.Server.ConsoleHTTPSListen == "" {
			evidence.Installation.Drift = append(evidence.Installation.Drift, "legacy_console_requires_cli_preparation")
		}
		return nil
	})
	return &view, evidence, err
}

func (i *Installer) applyAdoption(ctx context.Context) (bootstrap.Resource, error) {
	observed, err := i.Observe(ctx)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	o := observed.(Observation)
	evidence := *o.Adoption
	view := o.adoptedRecord

	// Local intent and the approved evidence are durable before any registration.
	*i.Record = *view
	if err = i.Journal.Save(i.Record); err != nil {
		return bootstrap.Resource{}, err
	}
	err = i.withStore(ctx, false, func(store storage.Store, cfg config.Config) error {
		evidence.Installation.State = bootstrap.Pending
		evidence.Installation.UpdatedAt = timeNow()
		if err = writeInstallation(ctx, store, evidence.Installation, evidence.RegistrationRevision); err != nil {
			return err
		}
		evidence.RegistrationRevision++
		application := core.New(store, cfg, nil)
		objects := map[string]bool{}
		definitions := []config.SystemApplicationConfig{}
		for _, key := range []string{"controller", "postgresql", "nginx", "nginx-sandbox"} {
			id := i.Config.Adoption.ServiceIDs[key]
			objects[id] = true
			environment := "prod"
			if key == "nginx-sandbox" {
				environment = "sandbox"
			}
			definitions = append(definitions, config.SystemApplicationConfig{Key: key, Name: map[string]string{"controller": "Controller", "postgresql": "PostgreSQL", "nginx": "NGINX", "nginx-sandbox": "NGINX Sandbox"}[key], Environment: environment, RuntimeID: i.Record.RuntimeID, ObjectIDs: []string{id}})
		}
		registration := cfg.Controller.SystemProject
		registration.Services = nil
		for _, service := range cfg.Controller.SystemProject.Services {
			if objects[service.ObjectID] {
				registration.Services = append(registration.Services, service)
			}
		}
		project, err := application.EnsureGatewayProject(ctx, registration, i.Record.ClusterID, objects)
		if err != nil {
			return err
		}
		if err = application.ReconcileGatewayApplications(ctx, project, i.Record.ClusterID, definitions, map[string]map[string]bool{i.Record.RuntimeID: objects}); err != nil {
			return err
		}
		evidence.Installation.State = bootstrap.Succeeded
		evidence.Installation.Revision = i.Record.Revision + 1
		return writeInstallation(ctx, store, evidence.Installation, evidence.RegistrationRevision)
	})
	if err != nil {
		return bootstrap.Resource{}, err
	}
	return bootstrap.Resource{Kind: "adoption", ID: i.Record.ID, SpecDigest: bootstrap.Digest(evidence)}, nil
}

func (e adoptionEvidence) proof() string {
	components := map[string]bootstrap.Resource{}
	for key, value := range e.Installation.Components {
		value.Version = 0
		components[key] = value
	}
	return bootstrap.Digest(struct {
		Components              map[string]bootstrap.Resource
		Database                bootstrap.DatabasePin
		Identity, Configuration string
	}{components, e.Installation.Database, e.IdentityDigest, e.ControllerConfiguration})
}

func (i *Installer) checkAdoption(ctx context.Context, s bootstrap.Step) error {
	if i.Config.Adoption == nil {
		return &bootstrap.Rejected{Reason: "adoption requires explicit pre-existing references"}
	}
	observed, err := i.Observe(ctx)
	if err != nil {
		return err
	}
	evidence := observed.(Observation).Adoption
	if s.State != bootstrap.Succeeded && s.Before != nil && evidence.proof() != s.Before.SpecDigest {
		return fmt.Errorf("approved adoption evidence changed")
	}
	if s.State == bootstrap.Succeeded {
		if evidence.RegistrationRevision == 0 || evidence.RegisteredState != bootstrap.Succeeded || evidence.RegisteredConfigDigest != i.Record.ConfigDigest {
			return storage.ErrConflict
		}
	}
	return nil
}
