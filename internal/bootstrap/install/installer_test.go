package install

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/swarm"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type fakeCluster struct {
	services    map[string]swarm.Service
	nodes       map[string]swarm.Node
	networks    map[string]map[string]any
	secrets     map[string]map[string]any
	next        int
	calls       []string
	fail        string
	afterUpdate bool
}
type fakeHost struct {
	cluster                                                       *fakeCluster
	name                                                          string
	active                                                        bool
	worker                                                        bool
	engineID, nodeID, gid, architecture, osType, state, clusterID string
	files                                                         map[string][]byte
	manifest                                                      []byte
	volumes                                                       map[string]map[string]any
	preflight                                                     string
	runErr                                                        error
	stream                                                        func(context.Context, string) (net.Conn, error)
}

func encode(value, out any) error {
	if out == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}
func (h *fakeHost) Privileged(command string) string { return command }
func (h *fakeHost) Stream(ctx context.Context, command string) (net.Conn, error) {
	if h.stream != nil {
		return h.stream(ctx, command)
	}
	return nil, errors.New("no fixture database transport")
}
func (h *fakeHost) Run(_ context.Context, command string, input []byte) ([]byte, error) {
	if h.runErr != nil {
		return nil, h.runErr
	}
	if strings.Contains(command, "image pull") {
		return nil, nil
	}
	if strings.Contains(command, "df -Pk") {
		if h.preflight != "" {
			return []byte(h.preflight), nil
		}
		return []byte("660\n10485760\n"), nil
	}
	if strings.Contains(command, "stat -c '%g'") {
		return []byte(h.gid), nil
	}
	if strings.Contains(command, "tar -xpf") {
		if h.files != nil {
			return nil, errors.New("target directory already exists")
		}
		h.files = map[string][]byte{}
		reader := tar.NewReader(bytes.NewReader(input))
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if header.Typeflag == tar.TypeDir {
				continue
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return nil, err
			}
			h.files[header.Name] = data
		}
		h.manifest = h.files["manifest.json"]
		return nil, nil
	}
	if strings.Contains(command, "sha256sum -c") {
		if h.files == nil {
			return nil, os.ErrNotExist
		}
		var manifest map[string]string
		if err := json.Unmarshal(h.manifest, &manifest); err != nil {
			return nil, err
		}
		for name, digest := range manifest {
			sum := sha256.Sum256(h.files[name])
			if hex.EncodeToString(sum[:]) != digest {
				return nil, errors.New("configuration drift")
			}
		}
		return h.manifest, nil
	}
	return nil, fmt.Errorf("unexpected fixed command")
}
func (h *fakeHost) Engine(_ context.Context, method, path string, input, out any) error {
	c := h.cluster
	c.calls = append(c.calls, method+" "+path)
	if c.fail == method+" "+path {
		return errors.New("injected Engine failure")
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return err
	}
	path = parsed.Path
	switch {
	case strings.HasPrefix(path, "/images/"):
		return encode(map[string]any{"Os": "linux", "Architecture": "amd64", "Config": map[string]any{"Labels": map[string]string{"io.nsc.bootstrap.contract": "1", "io.nsc.bootstrap.protocol": "2", "io.nsc.bootstrap.schema": "18"}, "Env": []string{"PG_MAJOR=18"}}}, out)
	case path == "/info":
		state := h.state
		if state == "" {
			state = "inactive"
			if h.active {
				state = "active"
			}
		}
		cluster := any(nil)
		if h.active && !h.worker {
			cluster = map[string]string{"ID": h.clusterID}
		}
		return encode(map[string]any{"ID": h.engineID, "Architecture": h.architecture, "OSType": h.osType, "Swarm": map[string]any{"NodeID": h.nodeID, "LocalNodeState": state, "ControlAvailable": h.active && !h.worker, "Cluster": cluster}}, out)
	case path == "/services" && method == "GET":
		values := []swarm.Service{}
		for _, v := range c.services {
			values = append(values, v)
		}
		return encode(values, out)
	case path == "/networks" && method == "GET":
		values := []map[string]any{}
		for _, v := range c.networks {
			values = append(values, v)
		}
		return encode(values, out)
	case path == "/swarm/init":
		var request struct{ Spec struct{ Name string } }
		encode(input, &request)
		if request.Spec.Name != "default" {
			return fmt.Errorf("swarm spec must be named default")
		}
		if h.active {
			return errors.New("already joined")
		}
		h.active = true
		h.nodeID = "node-" + h.name
		c.nodes[h.nodeID] = swarm.Node{ID: h.nodeID, Meta: swarm.Meta{Version: swarm.Version{Index: 1}}, Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager}}
		return encode(h.nodeID, out)
	case path == "/swarm":
		return encode(map[string]any{"ID": h.clusterID, "JoinTokens": map[string]string{"Manager": "isolated-join-token", "Worker": "isolated-worker-token"}}, out)
	case path == "/swarm/join":
		var join struct{ JoinToken string }
		encode(input, &join)
		h.worker = join.JoinToken == "isolated-worker-token"
		h.active = true
		h.nodeID = "node-" + h.name
		role := swarm.NodeRoleManager
		if h.worker {
			role = swarm.NodeRoleWorker
		}
		c.nodes[h.nodeID] = swarm.Node{ID: h.nodeID, Meta: swarm.Meta{Version: swarm.Version{Index: 1}}, Spec: swarm.NodeSpec{Role: role}}
		return nil
	case strings.HasPrefix(path, "/nodes/"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		node, ok := c.nodes[parts[1]]
		if !ok {
			return os.ErrNotExist
		}
		if method == "POST" {
			version, _ := strconv.ParseUint(parsed.Query().Get("version"), 10, 64)
			if node.Version.Index != version {
				return errors.New("stale node version")
			}
			if err = encode(input, &node.Spec); err != nil {
				return err
			}
			node.Version.Index++
			c.nodes[node.ID] = node
			return nil
		}
		return encode(node, out)
	case path == "/networks/create":
		c.next++
		id := fmt.Sprintf("network-%d", c.next)
		var spec map[string]any
		encode(input, &spec)
		spec["Id"] = id
		c.networks[id] = spec
		return encode(map[string]string{"Id": id}, out)
	case strings.HasPrefix(path, "/networks/"):
		value, ok := c.networks[strings.TrimPrefix(path, "/networks/")]
		if !ok {
			return os.ErrNotExist
		}
		return encode(value, out)
	case path == "/secrets/create":
		c.next++
		id := fmt.Sprintf("secret-%d", c.next)
		var spec map[string]any
		encode(input, &spec)
		c.secrets[id] = map[string]any{"ID": id, "Spec": spec}
		return encode(map[string]string{"ID": id}, out)
	case strings.HasPrefix(path, "/secrets/"):
		value, ok := c.secrets[strings.TrimPrefix(path, "/secrets/")]
		if !ok {
			return os.ErrNotExist
		}
		return encode(value, out)
	case path == "/volumes/create":
		c.next++
		id := fmt.Sprintf("anonymous-volume-%d", c.next)
		var spec map[string]any
		encode(input, &spec)
		if spec["Name"] != nil {
			return errors.New("fixture requires a fresh anonymous volume receipt")
		}
		spec["Name"] = id
		h.volumes[id] = spec
		return encode(spec, out)
	case strings.HasPrefix(path, "/volumes/"):
		value, ok := h.volumes[strings.TrimPrefix(path, "/volumes/")]
		if !ok {
			return os.ErrNotExist
		}
		return encode(value, out)
	case path == "/services/create":
		var spec swarm.ServiceSpec
		if err = encode(input, &spec); err != nil {
			return err
		}
		c.next++
		id := fmt.Sprintf("service-%d", c.next)
		c.services[id] = swarm.Service{ID: id, Meta: swarm.Meta{Version: swarm.Version{Index: 1}}, Spec: spec}
		return encode(map[string]string{"ID": id}, out)
	case strings.HasPrefix(path, "/services/"):
		parts := strings.Split(strings.Trim(path, "/"), "/")
		value, ok := c.services[parts[1]]
		if !ok {
			return os.ErrNotExist
		}
		if method == "POST" {
			version, _ := strconv.ParseUint(parsed.Query().Get("version"), 10, 64)
			if version != value.Version.Index {
				return errors.New("stale service version")
			}
			if err = encode(input, &value.Spec); err != nil {
				return err
			}
			value.Version.Index++
			c.services[value.ID] = value
			if c.afterUpdate {
				return context.DeadlineExceeded
			}
			return nil
		}
		return encode(value, out)
	case path == "/tasks":
		values := []swarm.Task{}
		for _, service := range c.services {
			if service.Spec.Labels["io.nsc.component"] == "postgresql" {
				values = append(values, swarm.Task{ID: "database-task", ServiceID: service.ID, NodeID: h.nodeID, Status: swarm.TaskStatus{State: swarm.TaskStateRunning, ContainerStatus: &swarm.ContainerStatus{ContainerID: "pinned-data-container"}}})
			}
		}
		return encode(values, out)
	default:
		return fmt.Errorf("unexpected Engine path: %s %s", method, path)
	}
}

type sqliteConnector struct {
	dsn  string
	fail error
}

// Unit-only Runtime repository over control documents. The real SQLStore keeps
// rejecting Swarm on SQLite. PostgreSQL storage and Swarm deployment are verified
// by the separate real bootstrap acceptance, never inferred from this fixture.
type unitSeedStore struct{ *storage.SQLStore }

func (s *unitSeedStore) CreateRuntime(ctx context.Context, value *model.Runtime) error {
	data, _ := json.Marshal(value)
	return s.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "fixture-runtime:" + value.ID, Kind: "fixture-runtime", Payload: data, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}}})
}
func (s *unitSeedStore) GetRuntime(ctx context.Context, id string) (model.Runtime, error) {
	var value model.Runtime
	d, err := s.GetControlDocument(ctx, "fixture-runtime:"+id)
	if err != nil {
		return value, err
	}
	err = json.Unmarshal(d.Payload, &value)
	return value, err
}

func (s sqliteConnector) Open(_ context.Context, _ config.Config, _ Engine, _ string, readOnly bool) (storage.Store, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	dsn := s.dsn
	if readOnly {
		dsn += "?_pragma=query_only(1)"
	}
	store, err := storage.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return &unitSeedStore{SQLStore: store}, nil
}

func installerFixture(t *testing.T, count int) (*Installer, *fakeCluster) {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "private")
	j, err := bootstrap.OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	image := "registry.example.test/nsc@sha256:" + strings.Repeat("a", 64)
	cfg := bootstrap.Config{Name: "fixture", Mode: "single", DataNode: "one", MaintenanceNode: "one", NodeDir: "/srv/nsc", ConsolePort: 8443, KnownHostsFile: "/keys/known_hosts", Images: bootstrap.Images{Controller: image, Gateway: image, PostgreSQL: image}}
	c := &fakeCluster{services: map[string]swarm.Service{}, nodes: map[string]swarm.Node{}, networks: map[string]map[string]any{}, secrets: map[string]map[string]any{}}
	hosts := map[string]Engine{}
	for index, name := range []string{"one", "two", "three"}[:count] {
		cfg.Nodes = append(cfg.Nodes, bootstrap.Node{Name: name, Address: fmt.Sprintf("192.0.2.%d", index+1), SSHUser: "root", SSHPort: 22, Manager: true})
		hosts[name] = &fakeHost{cluster: c, name: name, engineID: "engine-" + name, gid: "900", architecture: "x86_64", osType: "linux", clusterID: "cluster-id", volumes: map[string]map[string]any{}}
	}
	if count > 1 {
		cfg.Mode = "multi"
	}
	r, err := j.Initialize(cfg, "fixture-machine")
	if err != nil {
		t.Fatal(err)
	}
	i := &Installer{Config: cfg, Record: &r, Journal: j, Directory: directory, Hosts: hosts, Database: sqliteConnector{dsn: filepath.Join(directory, "fixture.sqlite")}}
	return i, c
}

func infrastructure(t *testing.T, i *Installer) {
	t.Helper()
	plan, err := i.Plan(t.Context(), bootstrap.Init)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan.Steps {
		if step.Key == "seed" {
			return
		}
		if err = i.Check(t.Context(), step); err != nil {
			t.Fatal(step.Key, err)
		}
		receipt, err := i.Apply(t.Context(), step)
		if err != nil {
			t.Fatal(step.Key, err)
		}
		i.Record.Resources[step.Key] = receipt
		if err = i.Check(t.Context(), bootstrap.Step{Key: step.Key, State: bootstrap.Succeeded, Receipt: &receipt}); err != nil {
			t.Fatal(step.Key, err)
		}
	}
	t.Fatal("seed step missing")
}

func TestSingleAndMultiInfrastructureOwnershipAndIdentity(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			i, c := installerFixture(t, count)
			infrastructure(t, i)
			if len(c.services) != 4 || len(c.networks) != 3 || len(c.secrets) != 1 || len(i.Hosts["one"].(*fakeHost).volumes) != 1 {
				t.Fatal("infrastructure shape differs")
			}
			for _, key := range []string{"controller", "nginx", "nginx-sandbox"} {
				before := i.Record.Resources["reserve:"+key]
				service := c.services[before.ID]
				if service.Spec.TaskTemplate.Placement.Constraints[0] != "node.id == 0000000000000000000000000" {
					t.Fatal("reserved workload started before initialization")
				}
				if key != "nginx-sandbox" && service.Spec.Mode.Global == nil {
					t.Fatal("global mode was not reserved")
				}
				receipt, err := i.Apply(t.Context(), bootstrap.Step{Key: "start:" + key})
				if err != nil {
					t.Fatal(err)
				}
				i.Record.Resources["start:"+key] = receipt
				if i.seedRecord().Components[key] != receipt {
					t.Fatal("installation projection retained a superseded service version")
				}
				if receipt.ID != before.ID || receipt.Version != before.Version+1 {
					t.Fatal("start replaced the service identity")
				}
				if err = i.Check(t.Context(), bootstrap.Step{Key: "reserve:" + key, Receipt: &before}); err != nil {
					t.Fatal("legitimate superseding receipt treated as external drift", err)
				}
			}
			pg := c.services[i.Record.Resources["service:postgresql"].ID]
			if pg.Spec.TaskTemplate.Placement.Constraints[0] != "node.id == node-one" || pg.Spec.Mode.Replicated == nil {
				t.Fatal("database not pinned")
			}
			for _, node := range i.Config.Nodes {
				host := i.Hosts[node.Name].(*fakeHost)
				for name := range host.files {
					if strings.HasSuffix(name, "ca.key") {
						t.Fatal("offline CA distributed")
					}
				}
				if bytes.Contains(host.files["controller.yaml"], []byte("auto_migrate: true")) {
					t.Fatal("Controller automatic migration enabled")
				}
				if !bytes.Contains(host.files["controller.yaml"], []byte("console_https_listen: :8443")) {
					t.Fatal("independent HTTPS missing")
				}
				if err := i.checkNodeFiles(t.Context(), node.Name, i.Record.Resources["configure:"+node.Name].SpecDigest); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
				t.Fatal("duplicate init created a second database")
			}
		})
	}
}

func TestOfflineInitializationRetainsDataUsersAndCertificate(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := i.seedRecord()
	if err := i.withStore(t.Context(), true, func(store storage.Store, cfg config.Config) error {
		runtime, err := store.GetRuntime(t.Context(), first.RuntimeID)
		if err != nil || runtime.ClusterID != first.ClusterID {
			return fmt.Errorf("runtime identity lost: %v", err)
		}
		certificate, err := store.GetCertificate(t.Context(), first.CertificateID)
		if err != nil || len(certificate.IPAddresses) != 1 {
			return fmt.Errorf("console asset missing: %v", err)
		}
		before, err := store.GetControlDocument(t.Context(), "community:identities")
		if err != nil {
			return err
		}
		if before.Kind != "identities" {
			return fmt.Errorf("administrator not initialized")
		}
		if err = store.WriteControlDocuments(t.Context(), []storage.ControlWrite{{Document: storage.ControlDocument{ID: "read-only-probe", Kind: "probe", Payload: json.RawMessage(`{}`)}}}); err == nil {
			return fmt.Errorf("read-only connector allowed writes")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := i.initializeDatabase(t.Context()); err != nil {
		t.Fatal("idempotent seed", err)
	}
	if i.seedRecord().Database != first.Database {
		t.Fatal("database/volume identity changed")
	}
	if err := i.finalizeDatabase(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := i.finalizeDatabase(t.Context()); err != nil {
		t.Fatal("idempotent finalization", err)
	}
	receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "finalize", State: bootstrap.Unknown})
	if err != nil || receipt == nil {
		t.Fatal("committed installation not reconciled", err)
	}
}

func TestPreflightRejectsUnsafeTargetsBeforeExternalMutation(t *testing.T) {
	for _, scenario := range []string{"unsupported-os", "unsupported-arch", "unresolved-swarm", "mixed-gid", "foreign-cluster", "occupied-name", "socket-permission", "disk", "port", "invalid-evidence", "missing-node"} {
		t.Run(scenario, func(t *testing.T) {
			i, c := installerFixture(t, 3)
			h := i.Hosts["one"].(*fakeHost)
			switch scenario {
			case "unsupported-os":
				h.osType = "windows"
			case "unsupported-arch":
				h.architecture = "unsupported"
			case "unresolved-swarm":
				h.state = "pending"
			case "mixed-gid":
				i.Hosts["two"].(*fakeHost).gid = "901"
			case "foreign-cluster":
				h.active = true
				h.nodeID = "one"
				other := i.Hosts["two"].(*fakeHost)
				other.active = true
				other.clusterID = "foreign"
			case "occupied-name":
				h.active = true
				h.nodeID = "one"
				c.networks["external"] = map[string]any{"Id": "external", "Name": i.name("ingress")}
			case "socket-permission":
				h.preflight = "600\n10485760"
			case "disk":
				h.preflight = "660\n1024"
			case "port":
				h.preflight = "660\n10485760\n00000000:0050"
			case "invalid-evidence":
				h.preflight = "unrecognized"
			case "missing-node":
				delete(i.Hosts, "two")
			}
			if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
				t.Fatal("unsafe target accepted")
			}
			for _, call := range c.calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("preflight performed an external mutation")
				}
			}
			if _, err := os.Stat(i.materialDir()); !os.IsNotExist(err) {
				t.Fatal("failed preflight created identities")
			}
		})
	}
}

func TestDriftAndUncertainServiceUpdateRemainFenced(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	key := "controller"
	before := i.Record.Resources["reserve:"+key]
	service := c.services[before.ID]
	service.Spec.TaskTemplate.ContainerSpec.Image = "external/image@sha256:" + strings.Repeat("b", 64)
	service.Version.Index++
	c.services[before.ID] = service
	if _, err := i.Apply(t.Context(), bootstrap.Step{Key: "start:" + key}); err == nil {
		t.Fatal("external spec overwritten")
	}
	if err := i.Check(t.Context(), bootstrap.Step{Key: "reserve:" + key, Receipt: &before}); err == nil {
		t.Fatal("drift reported as healthy")
	}
	service.Spec = i.serviceSpec(key, "900", true)
	service.Version.Index = before.Version
	c.services[before.ID] = service
	c.afterUpdate = true
	partial, err := i.Apply(t.Context(), bootstrap.Step{Key: "start:" + key})
	if err == nil || partial.ID != before.ID {
		t.Fatal("lost response discarded known service identity")
	}
	resolved, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "start:" + key, State: bootstrap.Unknown, Receipt: &partial})
	if err != nil || resolved == nil || resolved.ID != before.ID || resolved.Version != before.Version+1 {
		t.Fatal("owned update did not reconcile", err)
	}
	if resolved, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "reserve:missing", State: bootstrap.Unknown}); err != nil || resolved != nil {
		t.Fatal("receipt-less creation inferred by name")
	}
	h := i.Hosts["one"].(*fakeHost)
	h.files["controller.yaml"] = []byte("external change")
	if err = i.checkNodeFiles(t.Context(), "one", i.Record.Resources["configure:one"].SpecDigest); err == nil {
		t.Fatal("external node file overwritten or ignored")
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "configure:one", State: bootstrap.Unknown}); err == nil {
		t.Fatal("changed configuration treated as completed distribution")
	}
	for _, step := range []string{"service:other", "reserve:other", "network:other", "secret:other", "volume:other", "swarm:other", "configure:other", "shell"} {
		if _, err = i.Apply(t.Context(), bootstrap.Step{Key: step}); err == nil {
			t.Fatal("unapproved target accepted", step)
		}
	}
}

func TestCompletedDistributionCanReconcileWithoutOverwriting(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "configure:one", State: bootstrap.Unknown})
	if err != nil || receipt == nil || *receipt != i.Record.Resources["configure:one"] {
		t.Fatal("complete distribution not recognized", err)
	}
	if _, err = i.Apply(t.Context(), bootstrap.Step{Key: "configure:one"}); err == nil {
		t.Fatal("existing directory overwritten")
	}
	if err = i.makeMaterials(); err == nil {
		t.Fatal("private identities regenerated")
	}
}

func TestVerificationPlanIsReadOnlyAndUnavailableMaintenanceIsExplicit(t *testing.T) {
	i, c := installerFixture(t, 1)
	infrastructure(t, i)
	before := len(c.calls)
	if _, err := i.Plan(t.Context(), bootstrap.Verify); err != nil {
		t.Fatal(err)
	}
	for _, call := range c.calls[before:] {
		if strings.HasPrefix(call, "POST ") {
			t.Fatal("verification plan changed infrastructure")
		}
	}
	for _, action := range []bootstrap.Action{bootstrap.Adopt, bootstrap.Upgrade, bootstrap.Rollback, bootstrap.Repair} {
		if _, err := i.Plan(t.Context(), action); err == nil {
			t.Fatal("unfinished maintenance action incorrectly advertised", action)
		}
	}
}

func TestAdoptionReferencesCannotInitializeAnotherSwarm(t *testing.T) {
	i, c := installerFixture(t, 1)
	i.Config.Adoption = &bootstrap.Adoption{ControllerConfigFile: "/private/existing-controller.yaml", ClusterID: "original-cluster", DataNodeID: "original-node", Volume: "original-volume", ServiceIDs: map[string]string{"controller": "original-controller", "postgresql": "original-database", "nginx": "original-gateway", "nginx-sandbox": "original-sandbox"}}
	i.Record.ConfigDigest = bootstrap.Digest(i.Config)
	if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
		t.Fatal("adoption settings initialized a replacement cluster")
	}
	for _, call := range c.calls {
		if strings.HasPrefix(call, "POST ") {
			t.Fatal("adoption preflight changed Swarm")
		}
	}
}
