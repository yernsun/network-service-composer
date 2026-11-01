package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/maintenance"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Engine interface {
	Engine(context.Context, string, string, any, any) error
	Run(context.Context, string, []byte) ([]byte, error)
	Privileged(string) string
	Stream(context.Context, string) (net.Conn, error)
}

type NodeInfo struct {
	ID           string `json:"ID"`
	Architecture string `json:"Architecture"`
	OSType       string `json:"OSType"`
	Swarm        struct {
		NodeID           string `json:"NodeID"`
		LocalNodeState   string `json:"LocalNodeState"`
		ControlAvailable bool   `json:"ControlAvailable"`
		Cluster          *struct {
			ID string `json:"ID"`
		} `json:"Cluster"`
	} `json:"Swarm"`
}

type Observation struct {
	adoptedRecord *bootstrap.Record         `json:"-"`
	Membership    map[string]swarm.Node     `json:"membership"`
	Topology      bootstrap.ManagerTopology `json:"topology"`
	Adoption      *adoptionEvidence         `json:"adoption,omitempty"`
	Unavailable   []string                  `json:"unavailable,omitempty"`
	Nodes         map[string]NodeInfo       `json:"nodes"`
	Services      []bootstrap.Resource      `json:"services"`
	Names         []string                  `json:"names"`
	GIDs          map[string]string         `json:"gids"`
	Ports         []uint32                  `json:"ports"`
}

type Installer struct {
	PrepareWeb                bool
	OriginalCACertificateFile string
	OriginalCAPrivateKeyFile  string
	MaintenanceRuntime        func(*bootstrap.Record, storage.Store) maintenance.Runtime
	Config                    bootstrap.Config
	Record                    *bootstrap.Record
	Journal                   *bootstrap.Journal
	Directory                 string
	Hosts                     map[string]Engine
	Database                  DatabaseConnector
	GatewayClient             *http.Client
	Target                    *bootstrap.Release
	Components                []string
	CredentialFile            string
	// The CLI executor is independent of Controller tasks. Web maintenance uses
	// a separate one-shot runner and never borrows a Controller request lifetime.
}

func (i *Installer) entry() Engine          { return i.Hosts[i.Config.MaintenanceNode] }
func (i *Installer) prefix() string         { return i.Config.Name + "-" + bootstrap.Digest(i.Record.ID)[:12] }
func (i *Installer) nodeDir() string        { return i.Config.NodeDir + "/" + i.Record.ID }
func (i *Installer) name(key string) string { return i.prefix() + "-" + key }

func (i *Installer) Observe(ctx context.Context) (any, error) {
	o := Observation{Membership: map[string]swarm.Node{}, Nodes: map[string]NodeInfo{}, Services: []bootstrap.Resource{}, Names: []string{}, GIDs: map[string]string{}}
	for _, n := range i.Config.Nodes {
		h := i.Hosts[n.Name]
		if h == nil {
			return nil, fmt.Errorf("node transport is missing")
		}
		var info NodeInfo
		if err := h.Engine(ctx, "GET", "/info", nil, &info); err != nil {
			if i.Config.Adoption == nil || n.Name == i.Config.MaintenanceNode || n.Name == i.Config.DataNode || !errors.Is(err, bootstrap.ErrNodeUnavailable) {
				return nil, err
			}
			registered, fallbackErr := i.offlineIdentity(ctx, n)
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			o.Nodes[n.Name] = registered
			o.Unavailable = append(o.Unavailable, n.Name)
			continue
		}
		if info.ID == "" || info.OSType != "linux" || (info.Architecture != "x86_64" && info.Architecture != "aarch64" && info.Architecture != "amd64" && info.Architecture != "arm64") {
			return nil, fmt.Errorf("node %s requires a supported Linux Docker Engine", n.Name)
		}
		if info.Swarm.LocalNodeState != "inactive" && info.Swarm.LocalNodeState != "active" {
			return nil, fmt.Errorf("node %s has an unresolved Swarm state", n.Name)
		}
		if info.Swarm.LocalNodeState == "active" {
			if n.Name == i.Config.MaintenanceNode && !info.Swarm.ControlAvailable {
				return nil, fmt.Errorf("maintenance entry is a Worker; use an authenticated Manager")
			}
			if info.Swarm.Cluster != nil && i.Record.ClusterID != "" && info.Swarm.Cluster.ID != i.Record.ClusterID {
				return nil, fmt.Errorf("node belongs to another Swarm cluster")
			}
		}
		gid, err := h.Run(ctx, h.Privileged("stat -c '%g' /var/run/docker.sock"), nil)
		if err != nil {
			return nil, err
		}
		o.GIDs[n.Name] = strings.TrimSpace(string(gid))
		o.Nodes[n.Name] = info
	}
	entry := o.Nodes[i.Config.MaintenanceNode]
	if entry.Swarm.ControlAvailable {
		var members []swarm.Node
		if err := i.entry().Engine(ctx, "GET", "/nodes", nil, &members); err != nil {
			return nil, err
		}
		for _, member := range members {
			o.Membership[member.ID] = member
		}
		o.Topology = managerTopology(members)
		var services []swarm.Service
		if err := i.entry().Engine(ctx, "GET", "/services", nil, &services); err != nil {
			return nil, err
		}
		for _, s := range services {
			o.Services = append(o.Services, serviceReceipt(s))
			o.Names = append(o.Names, s.Spec.Name)
			if s.Spec.EndpointSpec != nil {
				for _, port := range s.Spec.EndpointSpec.Ports {
					if port.PublishedPort > 0 {
						o.Ports = append(o.Ports, port.PublishedPort)
					}
				}
			}
		}
		var networks []struct {
			Name string `json:"Name"`
			ID   string `json:"Id"`
		}
		if err := i.entry().Engine(ctx, "GET", "/networks", nil, &networks); err != nil {
			return nil, err
		}
		for _, network := range networks {
			o.Names = append(o.Names, network.Name)
		}
	}
	sort.Slice(o.Services, func(a, b int) bool { return o.Services[a].ID < o.Services[b].ID })
	sort.Strings(o.Names)
	sort.Slice(o.Ports, func(a, b int) bool { return o.Ports[a] < o.Ports[b] })
	if i.Config.Adoption != nil {
		view, evidence, err := i.adoptionView(ctx, o)
		if err != nil {
			return nil, err
		}
		o.Adoption = &evidence
		o.adoptedRecord = view.Record
	}
	return o, nil
}

func serviceReceipt(s swarm.Service) bootstrap.Resource {
	image := ""
	if s.Spec.TaskTemplate.ContainerSpec != nil {
		image = s.Spec.TaskTemplate.ContainerSpec.Image
	}
	return bootstrap.Resource{Kind: "service", ID: s.ID, Version: s.Version.Index, SpecDigest: bootstrap.Digest(s.Spec), Image: image}
}
func (i *Installer) inspectService(ctx context.Context, id string) (swarm.Service, error) {
	var service swarm.Service
	err := i.entry().Engine(ctx, "GET", "/services/"+url.PathEscape(id), nil, &service)
	return service, err
}

func (i *Installer) createService(ctx context.Context, spec swarm.ServiceSpec) (bootstrap.Resource, error) {
	var response struct {
		ID string `json:"ID"`
	}
	input, err := i.authorizedSpec(spec)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	if err := i.entry().Engine(ctx, "POST", "/services/create", input, &response); err != nil {
		// A partially decoded successful response can retain its actual ID.
		// Reconciliation still requires the exact intended specification.
		return bootstrap.Resource{Kind: "service", ID: response.ID, SpecDigest: bootstrap.Digest(spec)}, err
	}
	service, err := i.inspectService(ctx, response.ID)
	if err != nil {
		return bootstrap.Resource{Kind: "service", ID: response.ID, SpecDigest: bootstrap.Digest(spec)}, err
	}
	return serviceReceipt(service), err
}

func (i *Installer) updateService(ctx context.Context, before bootstrap.Resource, spec swarm.ServiceSpec) (bootstrap.Resource, error) {
	actual, err := i.inspectService(ctx, before.ID)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	if actual.Version.Index != before.Version || bootstrap.Digest(actual.Spec) != before.SpecDigest {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "service changed since its recorded receipt"}
	}
	path := fmt.Sprintf("/services/%s/update?version=%d", url.PathEscape(before.ID), before.Version)
	input, err := i.authorizedSpec(spec)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	if err = i.entry().Engine(ctx, "POST", path, input, nil); err != nil {
		return bootstrap.Resource{Kind: "service", ID: before.ID, SpecDigest: bootstrap.Digest(spec)}, err
	}
	actual, err = i.inspectService(ctx, before.ID)
	return serviceReceipt(actual), err
}

func (i *Installer) latest(id string) bootstrap.Resource {
	var result bootstrap.Resource
	for key, r := range i.Record.Resources {
		if strings.HasPrefix(key, "intent:") {
			continue
		}
		if r.ID == id && r.Version >= result.Version {
			result = r
		}
	}
	return result
}

func (i *Installer) validateObservation(o Observation) error {
	if err := requirePlacement(o, i.Config); err != nil {
		return err
	}
	entry := o.Nodes[i.Config.MaintenanceNode]
	cluster := ""
	if entry.Swarm.Cluster != nil {
		cluster = entry.Swarm.Cluster.ID
	}
	for _, n := range i.Config.Nodes {
		info := o.Nodes[n.Name]
		if info.Swarm.LocalNodeState == "active" {
			if info.Swarm.Cluster != nil && info.Swarm.Cluster.ID != cluster {
				return fmt.Errorf("inventory contains nodes from different clusters")
			}
			if cluster == "" {
				return fmt.Errorf("cannot join an existing Swarm through an inactive entry")
			}
			if n.Manager != info.Swarm.ControlAvailable {
				return fmt.Errorf("configured roles disagree with existing topology; adoption never changes roles")
			}
		}
		if n.Manager && o.GIDs[n.Name] != o.GIDs[i.Config.MaintenanceNode] {
			return fmt.Errorf("Docker socket GID differs across nodes; non-root Controller deployment is blocked")
		}
	}
	for _, port := range o.Ports {
		if i.reservedPort(uint64(port)) {
			return fmt.Errorf("an existing Swarm service owns a required installation port")
		}
	}
	for _, name := range o.Names {
		if strings.HasPrefix(name, i.prefix()+"-") {
			return fmt.Errorf("installation resource name conflicts with an existing object")
		}
	}
	return nil
}

func (i *Installer) Plan(ctx context.Context, action bootstrap.Action) (bootstrap.Plan, error) {
	if err := i.Config.Validate(); err != nil {
		return bootstrap.Plan{}, err
	}
	if bootstrap.Digest(i.Config) != i.Record.ConfigDigest {
		return bootstrap.Plan{}, fmt.Errorf("configuration differs from the durable installation record")
	}
	if action == bootstrap.Repair || action == bootstrap.Upgrade || action == bootstrap.Rollback {
		if i.PrepareWeb {
			return i.PlanWebPreparation(ctx, action)
		}
		return i.PlanMaintenance(ctx, action)
	}
	observation, err := i.Observe(ctx)
	if err != nil {
		return bootstrap.Plan{}, err
	}
	o := observation.(Observation)
	if action == bootstrap.Init {
		if err := i.metadataBoundary(); err != nil {
			return bootstrap.Plan{}, err
		}
		if len(i.Record.Resources) > 0 {
			return bootstrap.Plan{}, fmt.Errorf("installation already has resources; use resume or repair")
		}
		if err = i.validateObservation(o); err != nil {
			return bootstrap.Plan{}, err
		}
		if err = i.preflight(ctx, o); err != nil {
			return bootstrap.Plan{}, err
		}
		if i.Config.Adoption != nil {
			return bootstrap.Plan{}, fmt.Errorf("adoption configuration cannot initialize resources")
		}
		steps := []bootstrap.Step{}
		add := func(key, description string) {
			steps = append(steps, bootstrap.Step{Key: key, Description: description})
		}
		add("images", "Pull immutable images and verify platform, bootstrap protocol and PostgreSQL major version")
		add("materials", "Create and retain independent offline identities and recovery credentials")
		add("swarm:"+i.Config.MaintenanceNode, "Verify or prepare the approved Swarm Manager")
		for _, n := range i.Config.Nodes {
			if n.Name != i.Config.MaintenanceNode {
				add("swarm:"+n.Name, "Verify or prepare the approved Swarm node")
			}
		}
		for _, network := range []string{"control", "ingress", "sandbox"} {
			add("network:"+network, "Create isolated installation overlay network")
		}
		add("secret:postgresql", "Create the database password Secret")
		add("volume:postgresql", "Create the PostgreSQL volume on the fixed data node")
		add("service:postgresql", "Start PostgreSQL pinned to its original node and volume")
		for _, key := range []string{"controller", "nginx", "nginx-sandbox"} {
			add("reserve:"+key, "Reserve the actual Service ID without starting NSC")
		}
		add("secret:maintenance", "Retain a separate narrowly scoped maintenance credential in a protected Secret")
		add("control", "Create the non-secret Swarm installation checkpoint")
		add("reserve:executor", "Reserve the independent one-shot executor on the original maintenance node")
		for _, n := range i.Config.Nodes {
			add("configure:"+n.Name, "Distribute node-local configurations and private runtime identities")
		}
		add("seed", "Wait for PostgreSQL TCP readiness, migrate once and initialize installation records")
		add("backup", "Retain a verified database archive with its matching offline recovery identities")
		add("start:controller", "Start Controllers with automatic migration disabled")
		add("controller-ready", "Verify election, authenticated inventory and system application registration")
		add("start:nginx", "Start production Gateways")
		add("start:nginx-sandbox", "Start the dedicated isolated Sandbox")
		add("gateways-ready", "Verify positive loaded Gateway revisions and the dedicated Sandbox heartbeat")
		add("validation:bootstrap", "Create a source-bound real Sandbox NGINX validation")
		add("validation-ready", "Verify the recorded Sandbox validation outcome")
		add("handoff", "Verify login, positive loaded configuration, NGINX and actual HTTPS access")
		add("finalize", "Persist the verified installation projection without changing runtime identities")
		plan, err := bootstrap.NewPlan(*i.Record, action, o, steps, timeNow())
		return i.scopePlan(plan, o), err
	}
	return i.maintenancePlan(ctx, action, o)
}

func (i *Installer) scopePlan(p bootstrap.Plan, o Observation) bootstrap.Plan {
	scope := bootstrap.PlanScope{Topology: o.Topology, Mode: i.Config.Mode, ClusterID: i.Record.ClusterID, Targets: []bootstrap.PlanTarget{}, Images: i.Config.Images, DataNode: i.Config.DataNode, DataVolume: i.Record.Resources["volume:postgresql"].ID, DatabaseSinglePointOfFailure: true}
	if scope.ClusterID == "" {
		if cluster := o.Nodes[i.Config.MaintenanceNode].Swarm.Cluster; cluster != nil {
			scope.ClusterID = cluster.ID
		}
	}
	for _, n := range i.Config.Nodes {
		actual := o.Nodes[n.Name]
		scope.Targets = append(scope.Targets, bootstrap.PlanTarget{Name: n.Name, Address: n.Address, Manager: n.Manager, NodeID: actual.Swarm.NodeID, EngineID: actual.ID})
	}
	return p.WithScope(scope)
}

// JSON round-tripping is used for Engine-owned opaque object types, never for
// executable commands or arbitrary caller-supplied service specifications.
func decodeResource(data []byte, out any) error { return json.Unmarshal(data, out) }
