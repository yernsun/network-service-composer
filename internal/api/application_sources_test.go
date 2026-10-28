package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestApplicationSourceDraftHTTPPersistenceAndAuthorization(t *testing.T) {
	s, h, runtime, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	app, _, _, err := s.core.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: runtime.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"}, Name: "source", ProjectName: "source", IdempotencyKey: "install", Actor: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	draftPath := "/applications/" + app.ID + "/draft"
	absent := managementRequest(t, h, http.MethodGet, draftPath, nil, nil)
	if absent.Code != 200 || absent.Header().Get("ETag") != "\"rev-0\"" {
		t.Fatal("absent draft must have a writable zero ETag", absent.Code, absent.Body.String())
	}
	in := core.ApplicationDraftInput{ApplicationSource: core.ApplicationSource{ComposeYAML: "services: [", RuntimeEnv: "TOKEN=private-source-marker\n"}, ApplicationRevision: app.Revision}
	headers := map[string]string{"If-Match": absent.Header().Get("ETag"), "Idempotency-Key": "save-source"}
	saved := responseData[core.ApplicationDraft](t, managementRequest(t, h, http.MethodPut, draftPath, in, headers), 200)
	fresh := responseData[core.ApplicationDraft](t, managementRequest(t, h, http.MethodGet, draftPath, nil, nil), 200)
	if !reflect.DeepEqual(saved, fresh) || fresh.RuntimeEnv != in.RuntimeEnv {
		t.Fatal("refresh lost saved draft")
	}
	if replay := responseData[core.ApplicationDraft](t, managementRequest(t, h, http.MethodPut, draftPath, in, headers), 200); !reflect.DeepEqual(replay, saved) {
		t.Fatal("duplicate save changed result")
	}
	viewer, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "source-viewer", Role: "viewer", Enabled: true, Password: "isolated-viewer-password"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/services/missing/source", "/services/missing/draft", draftPath, "/applications/" + app.ID + "/source", "/applications/" + app.ID + "/revisions/" + app.DesiredRevisionID + "/source"} {
		response := managementRequest(t, h, http.MethodGet, path, nil, map[string]string{"Authorization": "Bearer " + token})
		if response.Code != 403 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("viewer can read confidential source", response.Code)
		}
	}
}
