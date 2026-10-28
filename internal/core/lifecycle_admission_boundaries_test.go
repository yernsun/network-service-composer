package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"strings"
	"testing"
)

type lifecycleAdmissionStore struct {
	storage.Store
	application model.Application
	service     model.Service
	runtime     model.Runtime
	failure     string
	queued      []model.DeploymentOperation
	revisions   map[string]model.ApplicationRevision
}

func (s *lifecycleAdmissionStore) GetApplicationRevision(_ context.Context, key string) (model.ApplicationRevision, error) {
	if v, ok := s.revisions[key]; ok {
		return v, nil
	}
	return model.ApplicationRevision{}, storage.ErrNotFound
}

func (s *lifecycleAdmissionStore) GetApplication(context.Context, string) (model.Application, error) {
	if s.failure == "application" {
		return model.Application{}, storage.ErrNotFound
	}
	return s.application, nil
}
func (s *lifecycleAdmissionStore) GetService(context.Context, string) (model.Service, error) {
	if s.failure == "service" {
		return model.Service{}, storage.ErrNotFound
	}
	return s.service, nil
}
func (s *lifecycleAdmissionStore) GetRuntime(context.Context, string) (model.Runtime, error) {
	if s.failure == "runtime" {
		return model.Runtime{}, storage.ErrNotFound
	}
	return s.runtime, nil
}
func (s *lifecycleAdmissionStore) CreateDeploymentOperation(_ context.Context, o *model.DeploymentOperation) (model.DeploymentOperation, error) {
	if s.failure == "queue" {
		return model.DeploymentOperation{}, storage.ErrConflict
	}
	s.queued = append(s.queued, *o)
	return *o, nil
}
func TestLifecycleAdmissionPreservesOwnershipAndRevisionChoice(t *testing.T) {
	for _, kind := range []string{"application", "service"} {
		for _, mode := range []string{"valid", "missing resource", "external", "missing application", "missing runtime", "global disabled", "runtime read only", "runtime disabled", "swarm sqlite", "invalid action", "queue failure", "start desired", "redeploy fallback", "start missing revision", "scale zero", "scale maximum", "scale negative", "scale excessive"} {
			if kind == "application" && (strings.HasPrefix(mode, "scale") || mode == "missing application") {
				continue
			}
			if kind == "service" && (strings.HasPrefix(mode, "start") || strings.HasPrefix(mode, "redeploy")) {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				c, rt, _ := controlFixture(t)
				f := &lifecycleAdmissionStore{Store: c.store, runtime: rt, application: model.Application{ID: "app", ProjectName: "project", RuntimeID: rt.ID, Managed: true, Status: model.ApplicationRunning, ActiveRevisionID: "active", DesiredRevisionID: "desired"}, service: model.Service{ID: "svc", RuntimeID: rt.ID, Managed: true, ApplicationID: "app", Name: "web", ServiceKey: "project/web"}}
				f.revisions = map[string]model.ApplicationRevision{}
				for _, key := range []string{"active", "desired"} {
					cipher, e := c.keyring.Encrypt([]byte("services:\n  web:\n    image: nginx:1\n"), revisionAAD(key, "resolved"))
					if e != nil {
						t.Fatal(e)
					}
					f.revisions[key] = model.ApplicationRevision{ID: key, EncryptedResolved: cipher}
				}
				c.store = f
				action := " stop "
				if kind == "service" {
					action = " restart "
				}
				replicas := 1
				want := "active"
				success := false
				switch mode {
				case "valid":
					success = true
				case "missing resource":
					f.failure = kind
				case "external":
					f.application.Managed = false
					f.service.Managed = false
				case "missing application":
					f.service.ApplicationID = ""
				case "missing runtime":
					f.failure = "runtime"
				case "global disabled":
					c.config.Management.Enabled = false
				case "runtime read only":
					f.runtime.ManagementEnabled = false
				case "runtime disabled":
					f.runtime.Enabled = false
				case "swarm sqlite":
					f.runtime.Type = model.RuntimeSwarm
				case "invalid action":
					action = "destroy all"
				case "queue failure":
					f.failure = "queue"
				case "start desired":
					action = "start"
					want = "desired"
					success = true
				case "redeploy fallback":
					action = "redeploy"
					f.application.DesiredRevisionID = ""
					success = true
				case "start missing revision":
					action = "start"
					f.application.DesiredRevisionID = ""
					f.application.ActiveRevisionID = ""
				case "scale zero":
					action = "scale"
					replicas = 0
					success = true
				case "scale maximum":
					action = "scale"
					replicas = 10000
					success = true
				case "scale negative":
					action = "scale"
					replicas = -1
				case "scale excessive":
					action = "scale"
					replicas = 10001
				}
				var op model.DeploymentOperation
				var e error
				if kind == "application" {
					op, e = c.QueueApplicationOperation(context.Background(), "app", action, "request")
				} else {
					op, e = c.QueueServiceOperation(context.Background(), "svc", action, replicas, "request")
				}
				if !success {
					if e == nil || len(f.queued) != 0 {
						t.Fatal("forbidden lifecycle operation queued", op, e)
					}
					if f.failure == "queue" && !errors.Is(e, storage.ErrConflict) {
						t.Fatal("queue conflict hidden", e)
					}
					return
				}
				if e != nil || len(f.queued) != 1 || op.ApplicationID != "app" || op.RuntimeID != rt.ID || op.Type != strings.ToUpper(strings.TrimSpace(action)) || op.RequestSHA256 == "" {
					t.Fatal("accepted operation lost identity", op, e)
				}
				if kind == "application" {
					if op.RevisionID != want {
						t.Fatal("incorrect lifecycle revision", op.RevisionID, want)
					}
				} else {
					if op.ServiceID != "svc" {
						t.Fatal("service scope lost")
					}
					raw, _, err := c.keyring.Decrypt(op.EncryptedInput, operationAAD(op.ID))
					if err != nil {
						t.Fatal(err)
					}
					var payload map[string]any
					if err = json.Unmarshal(raw, &payload); err != nil {
						t.Fatal(err)
					}
					if payload["service_key"] != "project/web" || payload["service_name"] != "web" {
						t.Fatal("executor target identity changed", payload)
					}
					if action == "scale" && payload["replicas"] != float64(replicas) {
						t.Fatal("replica decision changed", payload)
					}
				}
			})
		}
	}
}
