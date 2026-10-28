package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
)

func TestConsoleReferencesRetainEnvironmentAndEntrypointBoundaries(t *testing.T) {
	for _, stage := range []string{"services", "domain HTTP", "domain HTTPS", "domain both", "domain neither", "empty IDs", "invalid kind", "query", "scan", "iteration", "environment query", "environment malformed"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("console reference read interrupted")
			kind := "services"
			ids := []string{"resource"}
			script := &queryScript{t: t}
			wantError := false
			wantCause := error(nil)
			q := &queryReply{contains: "FROM services", columns: []string{"id", "name", "runtime"}, values: [][]driver.Value{{"resource", "Application", "runtime"}}}
			switch stage {
			case "domain HTTP", "domain HTTPS", "domain both", "domain neither":
				kind = "domains"
				q = &queryReply{contains: "FROM domains", columns: []string{"id", "host", "http", "https"}, values: [][]driver.Value{{"resource", "example.test", stage == "domain HTTP" || stage == "domain both", stage == "domain HTTPS" || stage == "domain both"}}}
			case "empty IDs":
				ids = nil
				q = nil
			case "invalid kind":
				kind = "invalid"
				q = nil
				wantError = true
			case "query":
				q.err = failure
				wantError = true
				wantCause = failure
			case "scan":
				q.columns = []string{"invalid"}
				q.values = [][]driver.Value{{nil}}
				wantError = true
			case "iteration":
				q.rowErr = failure
				wantError = true
				wantCause = failure
			}
			if q != nil {
				script.replies = append(script.replies, q)
			}
			if stage == "services" || stage == "environment query" || stage == "environment malformed" {
				claims := &queryReply{contains: "FROM services", columns: []string{"service", "payload"}, values: [][]driver.Value{{"resource", `{"environment_id":"environment"}`}}}
				if stage == "environment query" {
					claims.err = failure
					wantError = true
					wantCause = failure
				}
				if stage == "environment malformed" {
					claims.values[0][1] = "{"
					wantError = true
				}
				script.replies = append(script.replies, claims)
				if stage == "services" {
					script.replies = append(script.replies, &queryReply{contains: "kind='environment'", columns: []string{"id", "payload"}, values: [][]driver.Value{{"environment", `{"name":"Production","project_id":"project","project_name":"Checkout"}`}}})
				}
			}
			result, e := storeWithScript(t, script).ConsoleReferences(context.Background(), kind, ids)
			if (e != nil) != wantError {
				t.Fatal("console reference result incorrect", result, e)
			}
			if wantCause != nil && !errors.Is(e, wantCause) {
				t.Fatal("reference failure hidden", e)
			}
			if wantError {
				if result != nil {
					t.Fatal("partial console references escaped", result)
				}
				return
			}
			if stage == "empty IDs" {
				if len(result) != 0 {
					t.Fatal(result)
				}
				return
			}
			ref := result["resource"]
			if ref.ID != "resource" {
				t.Fatal("reference identity lost", ref)
			}
			if kind == "services" {
				if ref.Name != "Application" || ref.RuntimeID != "runtime" || ref.Environment == nil || *ref.Environment != (model.EnvironmentReference{ID: "environment", Name: "Production", ProjectID: "project", ProjectName: "Checkout"}) {
					t.Fatal("environment reference missing", ref)
				}
			} else {
				if ref.Host != "example.test" || ref.Name != ref.Host {
					t.Fatal(ref)
				}
				want := 0
				if stage == "domain both" {
					want = 2
				} else if stage != "domain neither" {
					want = 1
				}
				if len(ref.Entrypoints) != want {
					t.Fatal("entrypoint scope invented", ref)
				}
			}
		})
	}
}
