package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestIsolationResourceExtractionDistinguishesExplicitSharing(t *testing.T) {
	raw := `volumes:
  inherited: {external: true}
  named: {name: shared-data}
  local: {}
networks:
  named: {name: shared-network}
services:
  web:
    ports:
      - "127.0.0.1:8080:80/tcp"
      - "8080:80"
      - "5353:53/udp"
      - "80"
      - {published: 9090, target: 90}
      - {published: 5353, target: 53, protocol: udp}
      - {target: 100}
`
	got := isolationResources(raw)
	want := []IsolationResource{{Kind: "networks", Name: "shared-network", SharedWith: []string{}}, {Kind: "ports", Name: "5353/udp", SharedWith: []string{}}, {Kind: "ports", Name: "8080/tcp", SharedWith: []string{}}, {Kind: "ports", Name: "9090/tcp", SharedWith: []string{}}, {Kind: "volumes", Name: "inherited", SharedWith: []string{}}, {Kind: "volumes", Name: "shared-data", SharedWith: []string{}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit isolation resources=%+v", got)
	}
	if got := isolationResources("services: [broken"); got != nil {
		t.Fatal("invalid document yielded trusted isolation facts")
	}
}

type isolationBoundaryStore struct {
	storage.Store
	applications []model.Application
	revision     model.ApplicationRevision
	stage        string
	failure      error
	pages        []storage.Page
}

func (s *isolationBoundaryStore) ListApplications(_ context.Context, runtime string, p storage.Page) ([]model.Application, error) {
	s.pages = append(s.pages, p)
	if runtime != "runtime" {
		return nil, errors.New("wrong runtime isolation scope")
	}
	if s.stage == "applications" {
		return nil, s.failure
	}
	result := []model.Application{}
	for _, a := range s.applications {
		if a.ID > p.After {
			result = append(result, a)
			if len(result) == p.Limit {
				break
			}
		}
	}
	return result, nil
}
func (s *isolationBoundaryStore) GetApplicationRevision(_ context.Context, key string) (model.ApplicationRevision, error) {
	if s.stage == "revision" {
		return model.ApplicationRevision{}, s.failure
	}
	if key != s.revision.ID {
		return model.ApplicationRevision{}, storage.ErrNotFound
	}
	return s.revision, nil
}

func TestEnvironmentIsolationScansCompleteRuntimeAndRejectsUnknownHistory(t *testing.T) {
	for _, stage := range []string{"shared", "applications", "revision", "corrupt resolved", "no explicit sharing"} {
		t.Run(stage, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx := context.Background()
			revision := model.ApplicationRevision{ID: "old-revision"}
			other := `volumes: {data: {name: shared}, other: {name: separate}}
networks: {network: {name: shared}}
services: {web: {ports: ["8080:80"]}}
`
			encrypted, e := c.keyring.Encrypt([]byte(other), revisionAAD(revision.ID, "resolved"))
			if e != nil {
				t.Fatal(e)
			}
			revision.EncryptedResolved = encrypted
			if stage == "corrupt resolved" {
				revision.EncryptedResolved = []byte("invalid")
			}
			s := &isolationBoundaryStore{Store: c.store, revision: revision, stage: stage, failure: errors.New("isolation evidence unavailable")}
			for i := 0; i < 201; i++ {
				s.applications = append(s.applications, model.Application{ID: fmt.Sprintf("app-%03d", i)})
			}
			s.applications[0].ActiveRevisionID = revision.ID
			s.applications[200].ActiveRevisionID = revision.ID
			c.store = s
			compose := `volumes: {data: {name: shared}}
services: {web: {ports: ["8080:80"]}}
`
			if stage == "no explicit sharing" {
				compose = "services: {web: {image: example.test/web:fixed}}"
			}
			got, e := c.environmentIsolation(ctx, Environment{ApplicationID: "app-000", RuntimeID: "runtime"}, compose)
			if stage == "shared" {
				if e != nil || len(got) != 2 || len(s.pages) != 2 || s.pages[1].After != "app-199" {
					t.Fatalf("incomplete isolation scan: %+v pages=%+v error=%v", got, s.pages, e)
				}
				for _, r := range got {
					if !reflect.DeepEqual(r.SharedWith, []string{"app-200"}) {
						t.Fatalf("wrong shared resource owner: %+v", r)
					}
				}
				return
			}
			if stage == "no explicit sharing" {
				if e != nil || len(got) != 0 || len(s.pages) != 0 {
					t.Fatal("local-only resources required unrelated history", e)
				}
				return
			}
			if e == nil || got != nil {
				t.Fatal("unknown history presented as isolated")
			}
			if stage != "corrupt resolved" && !errors.Is(e, s.failure) {
				t.Fatal("isolation read error lost", e)
			}
		})
	}
}
