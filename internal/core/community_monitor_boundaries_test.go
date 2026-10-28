package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type communityScanStore struct {
	storage.Store
	gateways     []model.Gateway
	releases     []storage.ControlDocument
	certificates []model.Certificate
	pages        map[string][]storage.Page
	failIssue    bool
	corrupt      bool
	scan         func() error
}

func (s *communityScanStore) ListGateways(_ context.Context, p storage.Page) ([]model.Gateway, error) {
	if s.scan != nil {
		return nil, s.scan()
	}
	s.pages["gateways"] = append(s.pages["gateways"], p)
	for i, g := range s.gateways {
		if g.ID > p.After {
			return s.gateways[i:min(i+p.Limit, len(s.gateways))], nil
		}
	}
	return nil, nil
}
func (s *communityScanStore) GetGateway(_ context.Context, key string) (model.Gateway, error) {
	for _, g := range s.gateways {
		if g.ID == key {
			return g, nil
		}
	}
	return model.Gateway{}, storage.ErrNotFound
}
func (s *communityScanStore) ListCertificates(_ context.Context, p storage.Page) ([]model.Certificate, error) {
	s.pages["certificates"] = append(s.pages["certificates"], p)
	for i, c := range s.certificates {
		if c.ID > p.After {
			return s.certificates[i:min(i+p.Limit, len(s.certificates))], nil
		}
	}
	return nil, nil
}
func (s *communityScanStore) ListControlDocuments(_ context.Context, kind, _ string, p storage.Page) ([]storage.ControlDocument, error) {
	if kind == "issue" && s.corrupt {
		return []storage.ControlDocument{{ID: "broken", Payload: json.RawMessage("{")}}, nil
	}
	if kind != "nginx-release" {
		return nil, nil
	}
	s.pages["releases"] = append(s.pages["releases"], p)
	for i, d := range s.releases {
		if d.ID > p.After {
			return s.releases[i:min(i+p.Limit, len(s.releases))], nil
		}
	}
	return nil, nil
}
func (s *communityScanStore) GetControlDocument(_ context.Context, key string) (storage.ControlDocument, error) {
	if strings.HasPrefix(key, "issue_") && s.failIssue {
		return storage.ControlDocument{}, errors.New("issue lookup unavailable")
	}
	for _, d := range s.releases {
		if d.ID == key {
			return d, nil
		}
	}
	return storage.ControlDocument{}, storage.ErrNotFound
}

func TestCommunityScanPaginatesEveryResourceAndPreservesReadFailures(t *testing.T) {
	c, _, _ := controlFixture(t)
	now := c.now()
	future := now.Add(60 * 24 * time.Hour)
	s := &communityScanStore{Store: c.store, pages: map[string][]storage.Page{}}
	for i := 0; i < 201; i++ {
		key := fmt.Sprintf("%04d", i)
		s.gateways = append(s.gateways, model.Gateway{ID: key, Enabled: false})
		s.certificates = append(s.certificates, model.Certificate{ID: key, Status: model.CertificateActive, NotAfter: &future})
		payload, err := json.Marshal(NginxRelease{Status: "SUCCEEDED", FinishedAt: &now})
		if err != nil {
			t.Fatal(err)
		}
		s.releases = append(s.releases, storage.ControlDocument{ID: key, Kind: "nginx-release", ParentID: "pool", Payload: payload})
	}
	c.store = s
	if err := c.RefreshCommunityIssues(context.Background()); err != nil {
		t.Fatal(err)
	}
	for kind, pages := range s.pages {
		if len(pages) != 2 || pages[1].After != "0199" {
			t.Fatalf("%s scan lost second page: %+v", kind, pages)
		}
	}
	s.corrupt = true
	if _, err := c.Issues(context.Background(), storage.Page{}); err == nil {
		t.Fatal("corrupt issue displayed as healthy")
	}
	s.gateways = nil
	s.failIssue = true
	if err := c.RefreshCommunityIssues(context.Background()); err == nil || !strings.Contains(err.Error(), "issue lookup") {
		t.Fatal("release issue failure hidden", err)
	}
	s.releases = nil
	if err := c.RefreshCommunityIssues(context.Background()); err == nil || !strings.Contains(err.Error(), "issue lookup") {
		t.Fatal("certificate issue failure hidden", err)
	}
}

func TestCommunityMonitorContinuesAfterFailureAndHonorsCancellation(t *testing.T) {
	for _, name := range []string{"successful scan", "canceled scan", "failed scan", "next tick"} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := controlFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			calls := 0
			s := &communityScanStore{Store: c.store, pages: map[string][]storage.Page{}}
			s.scan = func() error {
				calls++
				if name != "next tick" || calls == 2 {
					cancel()
				}
				if name == "failed scan" || name == "next tick" {
					return errors.New("temporary scan failure")
				}
				if name == "canceled scan" {
					return context.Canceled
				}
				return nil
			}
			c.store = s
			if err := (CommunityMonitor{Core: c}).Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("monitor failed to stop", err)
			}
			want := 1
			if name == "next tick" {
				want = 2
			}
			if calls != want {
				t.Fatalf("scan count=%d want=%d", calls, want)
			}
		})
	}
}
