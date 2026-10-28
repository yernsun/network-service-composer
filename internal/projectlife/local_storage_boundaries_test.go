package projectlife

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

// Local retention evidence uses the real Engine SDK against this private HTTP
// protocol fixture. Its only writes are response bodies; it never opens Docker.
func TestLocalStorageEvidenceRequiresCompleteIdentityAndInventory(t *testing.T) {
	for _, scenario := range []string{"complete", "foreign node", "volume inventory unavailable", "invalid volume identity", "image inventory unavailable", "cached image unavailable", "cached image config missing", "container inventory unavailable", "container inspect unavailable", "container config missing", "container image unavailable", "container image config missing"} {
		t.Run(scenario, func(t *testing.T) {
			created := time.Now().UTC().Truncate(time.Second)
			var requests []string
			var requestMu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if strings.HasPrefix(path, "/v") {
					path = path[strings.Index(path[1:], "/")+1:]
				}
				requestMu.Lock()
				requests = append(requests, r.Method+" "+path)
				requestMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
				fail := func() { w.WriteHeader(503); encode(map[string]string{"message": "inventory unavailable"}) }
				switch path {
				case "/info":
					node := "worker"
					if scenario == "foreign node" {
						node = "foreign"
					}
					encode(map[string]any{"Swarm": map[string]string{"NodeID": node}})
				case "/volumes":
					if scenario == "volume inventory unavailable" {
						fail()
						return
					}
					at := created.Format(time.RFC3339Nano)
					if scenario == "invalid volume identity" {
						at = "unknown"
					}
					encode(map[string]any{"Volumes": []any{map[string]any{"Name": "stack_data", "Driver": "local", "CreatedAt": at, "Labels": map[string]string{"com.docker.stack.namespace": "stack"}}, map[string]string{"Name": "external", "Driver": "nfs", "CreatedAt": "unknown"}}})
				case "/images/json":
					if scenario == "image inventory unavailable" {
						fail()
						return
					}
					if r.URL.Query().Get("all") != "1" {
						t.Error("cached image inventory omitted unused images")
					}
					encode([]any{map[string]any{"Id": "cached", "RepoDigests": []string{"db@sha256:cached"}}})
				case "/images/cached/json", "/images/running/json":
					cached := path == "/images/cached/json"
					if cached && (scenario == "cached image unavailable") || !cached && (scenario == "container image unavailable") {
						fail()
						return
					}
					v := map[string]any{"Id": "cached", "RepoDigests": []string{"db@sha256:cached"}, "Config": map[string]any{"Volumes": map[string]any{"/data": map[string]any{}}}}
					if cached && (scenario == "cached image config missing") || !cached && (scenario == "container image config missing") {
						delete(v, "Config")
					}
					encode(v)
				case "/containers/json":
					if scenario == "container inventory unavailable" {
						fail()
						return
					}
					if r.URL.Query().Get("all") != "1" {
						t.Error("retention omitted stopped containers")
					}
					encode([]any{map[string]string{"Id": "db"}})
				case "/containers/db/json":
					if scenario == "container inspect unavailable" {
						fail()
						return
					}
					v := map[string]any{"Id": "db", "Image": "running", "Config": map[string]string{"Image": "db:fixed"}, "State": map[string]bool{"Running": true}, "Mounts": []any{map[string]string{"Type": "volume", "Name": "stack_data", "Destination": "/data"}}}
					if scenario == "container config missing" {
						delete(v, "Config")
					}
					encode(v)
				default:
					t.Error("unexpected SDK path", path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			cli, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.54"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			value, err := localNodeReader(cli)(t.Context(), "worker")
			if scenario != "complete" {
				if err == nil {
					t.Fatal("partial storage evidence accepted")
				}
				return
			}
			if err != nil || !value.Complete || value.NodeID != "worker" || value.At.IsZero() || !value.Volumes["stack_data"].Equal(created) || value.VolumeOwners["stack_data"] != "stack" {
				t.Fatal("local identity incomplete", err, value)
			}
			if _, ok := value.Volumes["external"]; ok {
				t.Fatal("remote volume driver admitted as local")
			}
			if !slices.Equal(value.Images["db:fixed"], []string{"/data"}) || !slices.Equal(value.Images["db@sha256:cached"], []string{"/data"}) || value.Containers["db"].Mounts[0].Name != "stack_data" {
				t.Fatal("image aliases or actual mounts lost")
			}
			requestMu.Lock()
			defer requestMu.Unlock()
			for _, request := range requests {
				if !strings.HasPrefix(request, "GET ") {
					t.Fatal("evidence collection mutated engine", request)
				}
			}
		})
	}
}

func TestLocalRetentionVerificationDoesNotCreateMissingOriginalVolume(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "original present", true: "original absent"}[missing], func(t *testing.T) {
			d, rt, app, _, f := runtimeFixture(t, true)
			// Add a volume and image inventory to the normal Engine protocol fixture.
			created := time.Now().UTC().Truncate(time.Second)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := r.URL.Path
				if strings.HasPrefix(path, "/v") {
					path = path[strings.Index(path[1:], "/")+1:]
				}
				if path == "/volumes" {
					vs := []any{}
					if !missing {
						vs = append(vs, map[string]any{"Name": "fixture-data", "Driver": "local", "CreatedAt": created.Format(time.RFC3339Nano), "Labels": map[string]string{"com.docker.stack.namespace": "fixture"}})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Volumes": vs})
					return
				}
				if path == "/images/json" || path == "/containers/json" {
					_ = json.NewEncoder(w).Encode([]any{})
					return
				}
				f.serve(w, r)
			}))
			defer server.Close()
			rt.Endpoint = server.URL
			retained := []RetainedVolume{{NodeID: "fixture-node", Name: "fixture-data", Owner: "fixture", CreatedAt: created}}
			err := d.VerifyRetention(context.Background(), rt, app, retained)
			if (err != nil) != missing {
				t.Fatal("local retained identity mismatch", err)
			}
			if len(f.requests) != 0 {
				t.Fatal("verification created or removed data", f.requests)
			}
			err = d.VerifyNewStorage(context.Background(), rt, app, "fixture-node", []string{"fixture-data"})
			if (err == nil) != missing {
				t.Fatal("first install adopted pre-existing local data", err)
			}
		})
	}
}
