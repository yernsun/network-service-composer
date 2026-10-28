package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/core"
)

func TestEnvironmentCapabilitiesHTTPReadOnlyAndNoStore(t *testing.T) {
	s, handler, runtime, _ := managedRuntimeFixture(t)
	ctx := context.Background()
	project, err := s.core.CreateProject(ctx, "Capabilities", "capabilities")
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.core.CreateEnvironment(ctx, project.ID, core.EnvironmentInput{Name: "prod", RuntimeID: runtime.ID})
	if err != nil {
		t.Fatal(err)
	}
	path := "/environments/" + env.ID + "/capabilities"
	res := managementRequest(t, handler, http.MethodGet, path, nil, nil)
	value := responseData[core.EnvironmentCapabilities](t, res, http.StatusOK)
	if res.Header().Get("Cache-Control") != "no-store" || value.Management.State != "CONFIGURED" || value.ImageUpdates.Reason != "application_required" || strings.Contains(res.Body.String(), runtime.Endpoint) {
		t.Fatal(res.Code, res.Body)
	}
	if res := managementRequest(t, handler, http.MethodGet, "/environments/missing/capabilities", nil, nil); res.Code != http.StatusNotFound {
		t.Fatal(res.Code, res.Body)
	}
	viewer, err := s.core.SaveUser(ctx, "", core.UserInput{Username: "capability-viewer", Role: "viewer", Enabled: true, Password: "viewer-password-for-tests"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.core.RotateUserToken(ctx, viewer.ID, viewer.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, authenticated := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1"+path, nil)
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusOK
		}
		if res.Code != want {
			t.Fatal(res.Code, res.Body)
		}
	}
}
