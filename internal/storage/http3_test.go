package storage_test

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTP3AcrossDatabases(t *testing.T) {
	for _, v := range []struct{ driver, dsn string }{{"sqlite", "file:" + filepath.Join(t.TempDir(), "http3.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"}, {"postgres", os.Getenv("NSC_TEST_POSTGRES_DSN")}, {"mysql", os.Getenv("NSC_TEST_MYSQL_DSN")}} {
		t.Run(v.driver, func(t *testing.T) {
			if v.dsn == "" {
				t.Skip("external database required")
			}
			ctx := context.Background()
			s, err := storage.Open(v.driver, v.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			pool := model.GatewayPool{ID: id.New(id.Pool), Name: "http3", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
			if err = s.CreatePool(ctx, &pool); err != nil {
				t.Fatal(err)
			}
			before, err := s.ResourceCounts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: pool.ID + ".example.com", TLSMode: model.TLSManaged, HTTPSEnabled: true, Enabled: true, Source: model.SourceAPI, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = s.CreateDomain(ctx, &domain); err != nil {
				t.Fatal(err)
			}
			read, err := s.GetDomain(ctx, domain.ID)
			if err != nil || read.HTTP3Enabled {
				t.Fatal(read, err)
			}
			domain.HTTP3Enabled = true
			if err = s.UpdateDomain(ctx, &domain, 1); err != nil {
				t.Fatal(err)
			}
			if err = s.UpdateDomain(ctx, &domain, 1); !errors.Is(err, storage.ErrPrecondition) {
				t.Fatal("missing optimistic conflict", err)
			}
			snap, err := s.BuildSnapshot(ctx, pool.ID)
			if err != nil || len(snap.Domains) != 1 || !snap.Domains[0].HTTP3Enabled {
				t.Fatal(snap, err)
			}
			read, err = s.GetDomain(ctx, domain.ID)
			if err != nil || !read.HTTP3Enabled {
				t.Fatal(read, err)
			}
			counts, err := s.ResourceCounts(ctx)
			if err != nil || counts["domains"] != before["domains"]+1 {
				t.Fatal(counts, err)
			}
			certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, EncryptionKeyID: "test", CertificatePEM: "test-certificate", EncryptedPrivateKey: []byte("encrypted"), Fingerprint: id.New("fingerprint"), Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = s.InstallCustomCertificate(ctx, &certificate, []string{domain.ID}, now); err != nil {
				t.Fatal(err)
			}
			read, err = s.GetDomain(ctx, domain.ID)
			if err != nil || !read.HTTP3Enabled {
				t.Fatal("certificate replacement reset HTTP/3", read, err)
			}
			gateway := model.Gateway{ID: id.New(id.Gateway), PoolID: pool.ID, Name: "h3", Enabled: true, HTTP3Supported: true, HTTP3AdvertisedPort: 18443, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if err = s.UpsertGateway(ctx, &gateway); err != nil {
				t.Fatal(err)
			}
			g, err := s.GetGateway(ctx, gateway.ID)
			if err != nil || !g.HTTP3Supported || g.HTTP3AdvertisedPort != 18443 {
				t.Fatal(g, err)
			}
			gateway.HTTP3Supported = false
			if err = s.UpsertGateway(ctx, &gateway); err != nil {
				t.Fatal(err)
			}
			g, err = s.GetGateway(ctx, gateway.ID)
			if err != nil || g.HTTP3Supported {
				t.Fatal("stale capability retained", g, err)
			}
		})
	}
}
