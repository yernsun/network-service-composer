package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/storage"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func (i *Installer) Check(ctx context.Context, s bootstrap.Step) error {
	if s.Receipt != nil {
		r := *s.Receipt
		switch r.Kind {
		case "verification":
			var err error
			switch s.Key {
			case "controller-ready":
				err = i.verifyController(ctx)
			case "gateways-ready":
				err = i.verifyGateways(ctx)
			case "validation-ready":
				err = i.verifyValidation(ctx)
			case "handoff":
				err = i.verifyHandoff(ctx)
			}
			if err != nil {
				return err
			}
		case "service":
			service, err := i.inspectService(ctx, r.ID)
			if err != nil {
				return err
			}
			latest := i.latest(r.ID)
			if service.ID != r.ID || bootstrap.Digest(service.Spec) != latest.SpecDigest || service.Version.Index != latest.Version {
				return fmt.Errorf("recorded service changed or disappeared")
			}
		case "backup":
			if err := i.checkBackup(r); err != nil {
				return err
			}
		case "materials":
			digest, err := i.materialDigest()
			if err != nil {
				return err
			}
			if digest != r.SpecDigest {
				return fmt.Errorf("identity materials changed")
			}
		case "swarm":
			var info NodeInfo
			if err := i.Hosts[strings.TrimPrefix(s.Key, "swarm:")].Engine(ctx, "GET", "/info", nil, &info); err != nil {
				return err
			}
			if info.Swarm.NodeID != r.ID || info.Swarm.LocalNodeState != "active" {
				return fmt.Errorf("Swarm node identity changed")
			}
		case "node-files":
			if err := i.checkNodeFiles(ctx, strings.TrimPrefix(s.Key, "configure:"), r.SpecDigest); err != nil {
				return err
			}
		case "volume":
			var actual struct {
				Name   string
				Labels map[string]string
			}
			if err := i.Hosts[i.Config.DataNode].Engine(ctx, "GET", "/volumes/"+url.PathEscape(r.ID), nil, &actual); err != nil {
				return err
			}
			if actual.Name != r.ID || actual.Labels["io.nsc.installation"] != i.Record.ID {
				return fmt.Errorf("database volume receipt no longer matches")
			}
		case "network", "secret":
			var actual struct {
				ID     string `json:"Id"`
				Labels map[string]string
				Spec   struct{ Labels map[string]string }
			}
			collection := "networks"
			if r.Kind == "secret" {
				collection = "secrets"
			}
			if err := i.entry().Engine(ctx, "GET", "/"+collection+"/"+url.PathEscape(r.ID), nil, &actual); err != nil {
				return err
			}
			labels := actual.Labels
			if r.Kind == "secret" {
				labels = actual.Spec.Labels
			}
			if actual.ID != r.ID || labels["io.nsc.installation"] != i.Record.ID {
				return fmt.Errorf("installation resource receipt no longer matches")
			}
		}
	}
	for _, node := range i.Config.Nodes {
		identity := i.Record.Nodes[node.Name]
		var actual NodeInfo
		if err := i.Hosts[node.Name].Engine(ctx, "GET", "/info", nil, &actual); err != nil {
			return err
		}
		if identity.EngineID != "" && identity.EngineID != actual.ID {
			return fmt.Errorf("Docker Engine changed after planning")
		}
		if identity.NodeID != "" && identity.NodeID != actual.Swarm.NodeID {
			return fmt.Errorf("Swarm node changed after planning")
		}
	}
	if i.Record.ClusterID != "" && s.Key != "materials" {
		var info NodeInfo
		if err := i.entry().Engine(ctx, "GET", "/info", nil, &info); err != nil {
			return err
		}
		if info.Swarm.Cluster == nil || info.Swarm.Cluster.ID != i.Record.ClusterID || !info.Swarm.ControlAvailable {
			return fmt.Errorf("authenticated entry is not a Manager of the installed cluster")
		}
	}
	return nil
}

func (i *Installer) Apply(ctx context.Context, s bootstrap.Step) (bootstrap.Resource, error) {
	parts := strings.SplitN(s.Key, ":", 2)
	kind := parts[0]
	key := ""
	if len(parts) == 2 {
		key = parts[1]
	}
	if kind == "service" || kind == "reserve" || kind == "start" {
		if key != "controller" && key != "nginx" && key != "nginx-sandbox" && key != "postgresql" {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "component is outside the installation allowlist"}
		}
	}
	if kind == "network" && key != "control" && key != "ingress" && key != "sandbox" {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "network is outside the installation allowlist"}
	}
	if (kind == "secret" || kind == "volume") && key != "postgresql" {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "resource is outside the installation allowlist"}
	}
	resource := func(kind, id string, value any) bootstrap.Resource {
		return bootstrap.Resource{Kind: kind, ID: id, SpecDigest: bootstrap.Digest(value)}
	}
	switch kind {
	case "images":
		return i.prepareImages(ctx)
	case "backup":
		return i.backupDatabase(ctx)
	case "materials":
		if err := i.makeMaterials(); err != nil {
			return bootstrap.Resource{}, err
		}
		digest, err := i.materialDigest()
		return bootstrap.Resource{Kind: "materials", ID: i.Record.ID, SpecDigest: digest}, err
	case "swarm":
		return i.prepareSwarm(ctx, key)
	case "network":
		var reply struct {
			ID string `json:"Id"`
		}
		err := i.entry().Engine(ctx, "POST", "/networks/create", map[string]any{"Name": i.name(key), "Driver": "overlay", "Attachable": true, "Options": map[string]string{"encrypted": ""}, "Labels": map[string]string{"io.nsc.installation": i.Record.ID}}, &reply)
		return resource("network", reply.ID, key), err
	case "secret":
		value, err := bootstrap.ReadPrivate(i.material("postgres-password"))
		if err != nil {
			return bootstrap.Resource{}, err
		}
		defer clear(value)
		var reply struct {
			ID string `json:"ID"`
		}
		err = i.entry().Engine(ctx, "POST", "/secrets/create", map[string]any{"Name": i.name(key), "Data": base64.StdEncoding.EncodeToString(value), "Labels": map[string]string{"io.nsc.installation": i.Record.ID}}, &reply)
		return resource("secret", reply.ID, bootstrap.Digest(value)), err
	case "volume":
		var reply struct {
			Name string `json:"Name"`
		}
		err := i.Hosts[i.Config.DataNode].Engine(ctx, "POST", "/volumes/create", map[string]any{"Driver": "local", "Labels": map[string]string{"io.nsc.installation": i.Record.ID}}, &reply)
		return resource("volume", reply.Name, i.Record.Nodes[i.Config.DataNode]), err
	case "service", "reserve":
		gid, err := i.socketGID(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		return i.createService(ctx, i.serviceSpec(key, gid, kind == "reserve"))
	case "configure":
		return i.distribute(ctx, key)
	case "seed":
		if err := i.initializeDatabase(ctx); err != nil {
			return bootstrap.Resource{}, err
		}
		return resource("database", i.Record.ID, i.seedRecord()), nil
	case "start":
		before, ok := i.Record.Resources["reserve:"+key]
		if !ok {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "service reservation receipt is missing"}
		}
		gid, err := i.socketGID(ctx)
		if err != nil {
			return bootstrap.Resource{}, err
		}
		return i.updateService(ctx, before, i.serviceSpec(key, gid, false))
	case "controller-ready":
		if err := i.verifyController(ctx); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Controller verification failed"}
		}
		return resource("verification", i.Record.ID+":controller", i.seedRecord()), nil
	case "validation":
		return i.createValidation(ctx)
	case "gateways-ready":
		if err := i.verifyGateways(ctx); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Gateway or Sandbox readiness is not verified"}
		}
		return resource("verification", i.Record.ID+":gateways", i.Record.ConfigDigest), nil
	case "validation-ready":
		if err := i.verifyValidation(ctx); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Sandbox validation failed"}
		}
		return resource("verification", i.Record.Resources["validation:bootstrap"].ID, "sandbox-nginx"), nil
	case "finalize":
		if err := i.finalizeDatabase(ctx); err != nil {
			return bootstrap.Resource{}, err
		}
		return resource("database", i.Record.ID, "verified-installation"), nil
	case "handoff":
		if err := i.verifyHandoff(ctx); err != nil {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "installation access or loading verification failed"}
		}
		return resource("verification", i.Record.ID+":handoff", i.seedRecord()), nil
	default:
		return i.applyMaintenance(ctx, s)
	}
}

func (i *Installer) socketGID(ctx context.Context) (string, error) {
	data, err := i.entry().Run(ctx, i.entry().Privileged("stat -c '%g' /var/run/docker.sock"), nil)
	value := strings.TrimSpace(string(data))
	_, parseErr := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return "", err
	}
	if parseErr != nil {
		return "", fmt.Errorf("invalid Docker socket GID")
	}
	if gid := i.Record.Nodes[i.Config.MaintenanceNode].SocketGID; gid != "" && gid != value {
		return "", fmt.Errorf("Docker socket group differs from its frozen preflight identity")
	}
	return value, nil
}

func (i *Installer) prepareSwarm(ctx context.Context, key string) (bootstrap.Resource, error) {
	node, err := i.selectedNode(key)
	if err != nil {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "node is outside the installation inventory"}
	}
	h := i.Hosts[key]
	var info NodeInfo
	if err := h.Engine(ctx, "GET", "/info", nil, &info); err != nil {
		return bootstrap.Resource{}, err
	}
	if old := i.Record.Nodes[key]; old.EngineID != "" && old.EngineID != info.ID {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Docker Engine identity changed"}
	}
	if i.Record.Nodes[key].NodeID == "" && info.Swarm.LocalNodeState == "active" {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "node joined a Swarm after planning"}
	}
	if info.Swarm.LocalNodeState == "inactive" {
		if key == i.Config.MaintenanceNode {
			var nodeID string
			if err := h.Engine(ctx, "POST", "/swarm/init", map[string]any{"ListenAddr": "0.0.0.0:2377", "AdvertiseAddr": node.Address, "Spec": map[string]any{"Name": "default"}}, &nodeID); err != nil {
				return bootstrap.Resource{}, err
			}
			identity := i.Record.Nodes[key]
			identity.NodeID = nodeID
			i.Record.Nodes[key] = identity
		} else {
			var cluster struct {
				ID         string
				JoinTokens struct{ Manager, Worker string }
			}
			if err := i.entry().Engine(ctx, "GET", "/swarm", nil, &cluster); err != nil {
				return bootstrap.Resource{}, err
			}
			if cluster.ID != i.Record.ClusterID {
				return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "Manager cluster identity changed"}
			}
			entry := i.Config.Nodes[0]
			for _, n := range i.Config.Nodes {
				if n.Name == i.Config.MaintenanceNode {
					entry = n
				}
			}
			token := cluster.JoinTokens.Worker
			if node.Manager {
				token = cluster.JoinTokens.Manager
			}
			if err := h.Engine(ctx, "POST", "/swarm/join", map[string]any{"ListenAddr": "0.0.0.0:2377", "AdvertiseAddr": node.Address, "RemoteAddrs": []string{net.JoinHostPort(entry.Address, "2377")}, "JoinToken": token}, nil); err != nil {
				return bootstrap.Resource{}, err
			}
		}
		if err := wait(ctx, func() error {
			if err := h.Engine(ctx, "GET", "/info", nil, &info); err != nil {
				return err
			}
			if info.Swarm.LocalNodeState != "active" || info.Swarm.NodeID == "" || (node.Manager && (!info.Swarm.ControlAvailable || info.Swarm.Cluster == nil)) {
				return fmt.Errorf("Swarm node is not ready")
			}
			return nil
		}); err != nil {
			return bootstrap.Resource{Kind: "swarm", ID: i.Record.Nodes[key].NodeID, SpecDigest: bootstrap.Digest(i.Record.Nodes[key])}, err
		}
	}
	if info.Swarm.LocalNodeState != "active" || info.Swarm.NodeID == "" {
		return bootstrap.Resource{}, fmt.Errorf("Swarm node identity is not ready")
	}
	if key == i.Config.MaintenanceNode {
		if !info.Swarm.ControlAvailable || info.Swarm.Cluster == nil {
			return bootstrap.Resource{}, fmt.Errorf("entry is not a ready Swarm Manager")
		}
		if i.Record.ClusterID != "" && i.Record.ClusterID != info.Swarm.Cluster.ID {
			return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "entry cluster identity differs"}
		}
		i.Record.ClusterID = info.Swarm.Cluster.ID
	}
	// The Manager's view is authoritative for worker ClusterID and role.
	var member swarm.Node
	if err := i.entry().Engine(ctx, "GET", "/nodes/"+url.PathEscape(info.Swarm.NodeID), nil, &member); err != nil {
		return bootstrap.Resource{}, err
	}
	if (member.Spec.Role == swarm.NodeRoleManager) != node.Manager {
		return bootstrap.Resource{}, &bootstrap.Rejected{Reason: "existing node role differs from the approved topology"}
	}
	if node.Manager {
		if member.Spec.Labels == nil {
			member.Spec.Labels = map[string]string{}
		}
		label := "io.nsc.controller." + i.Record.ID
		if member.Spec.Labels[label] != "true" {
			member.Spec.Labels[label] = "true"
			if err := i.entry().Engine(ctx, "POST", fmt.Sprintf("/nodes/%s/update?version=%d", member.ID, member.Version.Index), member.Spec, nil); err != nil {
				return bootstrap.Resource{}, err
			}
		}
	}
	identity := i.Record.Nodes[key]
	identity.NodeID = info.Swarm.NodeID
	identity.EngineID = info.ID
	i.Record.Nodes[key] = identity
	return bootstrap.Resource{Kind: "swarm", ID: info.Swarm.NodeID, SpecDigest: bootstrap.Digest(identity)}, nil
}

func (i *Installer) Reconcile(ctx context.Context, s bootstrap.Step) (*bootstrap.Resource, error) {
	// Receipt-less creation cannot be inferred from labels, names or RUNNING.
	// Read-only seed/verification steps may be repeated, using recorded IDs.
	if s.Receipt != nil && s.Receipt.Kind == "swarm" && s.Receipt.ID != "" {
		name := strings.TrimPrefix(s.Key, "swarm:")
		node, err := i.selectedNode(name)
		if err != nil {
			return nil, err
		}
		var actual NodeInfo
		if err = i.Hosts[name].Engine(ctx, "GET", "/info", nil, &actual); err != nil {
			return nil, err
		}
		identity := i.Record.Nodes[name]
		if actual.ID != identity.EngineID || actual.Swarm.NodeID != s.Receipt.ID || actual.Swarm.LocalNodeState != "active" || node.Manager && (!actual.Swarm.ControlAvailable || actual.Swarm.Cluster == nil) {
			return nil, fmt.Errorf("Swarm initialization receipt is not yet confirmed")
		}
		if name == i.Config.MaintenanceNode {
			i.Record.ClusterID = actual.Swarm.Cluster.ID
		}
		return nil, &bootstrap.RetrySafe{Reason: "continue node preparation using the acknowledged Swarm Node ID"}
	}
	if s.Receipt != nil && s.Receipt.Kind == "service" && s.Receipt.ID != "" {
		actual, err := i.inspectService(ctx, s.Receipt.ID)
		if err != nil {
			return nil, err
		}
		if bootstrap.Digest(actual.Spec) != s.Receipt.SpecDigest {
			return nil, fmt.Errorf("uncertain service result differs from its intended configuration")
		}
		r := serviceReceipt(actual)
		return &r, nil
	}
	if s.Key == "backup" {
		if _, err := i.databaseContainer(ctx); err != nil {
			return nil, err
		}
		return nil, &bootstrap.RetrySafe{Reason: "repeat the read-only archive of the same pinned database, retaining previous private files"}
	}
	if s.Key == "images" {
		return nil, &bootstrap.RetrySafe{Reason: "repeat immutable image preparation without changing installation resources"}
	}
	if s.Key == "materials" {
		marker, err := bootstrap.ReadPrivate(i.material("complete.json"))
		if err != nil {
			return nil, err
		}
		digest, err := i.materialDigest()
		if err != nil || string(marker) != digest {
			return nil, fmt.Errorf("identity creation is incomplete or materials changed")
		}
		r := bootstrap.Resource{Kind: "materials", ID: i.Record.ID, SpecDigest: digest}
		return &r, nil
	}
	if strings.HasPrefix(s.Key, "configure:") {
		name := strings.TrimPrefix(s.Key, "configure:")
		node, err := i.selectedNode(name)
		if err != nil {
			return nil, err
		}
		files, err := i.nodeFiles(node)
		if err != nil {
			return nil, err
		}
		_, digest, err := nodeArchive(files)
		if err != nil {
			return nil, err
		}
		if err = i.checkNodeFiles(ctx, name, digest); err != nil {
			return nil, err
		}
		r := bootstrap.Resource{Kind: "node-files", ID: i.Record.ID + ":" + name, SpecDigest: digest}
		return &r, nil
	}
	if s.Key == "seed" || s.Key == "finalize" {
		var result *bootstrap.Resource
		err := i.withStore(ctx, true, func(store storage.Store, cfg config.Config) error {
			d, err := store.GetControlDocument(ctx, bootstrap.InstallationDocumentID)
			if err != nil {
				return err
			}
			var saved bootstrap.Installation
			if err = json.Unmarshal(d.Payload, &saved); err != nil {
				return err
			}
			if saved.ID != i.Record.ID || saved.ClusterID != i.Record.ClusterID || saved.ConfigDigest != i.Record.ConfigDigest {
				return fmt.Errorf("database installation identity differs")
			}
			if s.Key == "seed" && saved.State == bootstrap.Pending {
				return &bootstrap.RetrySafe{Reason: "continue the verified partial offline initialization using its stable IDs"}
			}
			if s.Key == "seed" && (saved.State == bootstrap.Running || saved.State == bootstrap.Succeeded) || s.Key == "finalize" && saved.State == bootstrap.Succeeded {
				r := bootstrap.Resource{Kind: "database", ID: i.Record.ID, SpecDigest: bootstrap.Digest(saved)}
				result = &r
			}
			return nil
		})
		return result, err
	}

	if s.Key == "controller-ready" || s.Key == "handoff" || s.Key == "gateways-ready" {
		return nil, &bootstrap.RetrySafe{Reason: "repeat read-only installation verification"}
	}
	if s.Key == "validation-ready" {
		return nil, &bootstrap.RetrySafe{Reason: "repeat the recorded read-only validation check"}
	}
	return nil, nil
}

func wait(ctx context.Context, check func() error) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last error
	for {
		if last = check(); last == nil {
			return nil
		}
		var rejected *bootstrap.Rejected
		if errors.As(last, &rejected) {
			return last
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("verification did not complete: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
