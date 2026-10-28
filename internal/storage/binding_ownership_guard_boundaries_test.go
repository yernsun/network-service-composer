package storage

import (
	"context"
	"errors"
	"github.com/yernsun/network-service-composer/internal/model"
	"strings"
	"testing"
	"time"
)

func TestBindingStorageRejectsForeignIdentityAndStaleSharedDomain(t *testing.T) {
	for _, mode := range []string{"discovery source", "foreign pool", "foreign service", "foreign domain", "stale domain", "stale route", "foreign certificate pool", "foreign certificate ID", "referenced foreign certificate"} {
		t.Run(mode, func(t *testing.T) {
			domain := model.Domain{ID: "domain", PoolID: "pool", CertificateID: "cert"}
			route := model.Route{ID: "route", DomainID: "domain", ServiceID: "service"}
			cert := &model.Certificate{ID: "cert", PoolID: "pool"}
			source := model.SourceAPI
			domainExpected, routeExpected := int64(1), int64(1)
			switch mode {
			case "discovery source":
				source = model.SourceDiscovery
			case "foreign pool":
				domain.PoolID = "foreign"
			case "foreign service":
				route.ServiceID = "foreign"
			case "foreign domain":
				route.DomainID = "foreign"
			case "stale domain":
				domainExpected = 2
			case "stale route":
				routeExpected = 2
			case "foreign certificate pool":
				cert.PoolID = "foreign"
			case "foreign certificate ID":
				cert.ID = "foreign"
			case "referenced foreign certificate":
				cert = nil
			}
			steps := []transactionBoundaryStep{boundaryRead("SELECT rt.pool_id,r.source FROM routes", []string{"pool", "source"}, "pool", string(source))}
			early := mode == "discovery source" || mode == "foreign pool" || mode == "foreign service" || mode == "foreign domain"
			if !early {
				steps = append(steps, boundaryRead("SELECT id FROM domains", []string{"id"}, "domain"))
				steps = append(steps, boundaryPlaneSteps(false)...)
				steps = append(steps, boundaryRead("SELECT certificate_id,revision FROM domains", []string{"certificate_id", "revision"}, "previous", int64(1)), boundaryRead("SELECT revision FROM routes", []string{"revision"}, int64(1)))
				if mode == "referenced foreign certificate" {
					steps = append(steps, boundaryRead("SELECT pool_id FROM certificates", []string{"pool"}, "foreign"))
				}
			}
			script := &queryScript{t: t, transactions: true}
			for _, step := range steps {
				script.replies = append(script.replies, step.query)
			}
			s := storeWithScript(t, script)
			e := s.UpdateServiceDomainBinding(context.Background(), "service", &domain, cert, &route, domainExpected, routeExpected)
			want := ErrConflict
			if strings.HasPrefix(mode, "stale") {
				want = ErrPrecondition
			}
			if !errors.Is(e, want) || script.execCalls != 0 || script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("unsafe binding identity wrote state", e, script.execCalls, script.commits)
			}
		})
	}
}
func TestManagedCertificateProvisionPreservesExistingSelection(t *testing.T) {
	for _, mode := range []string{"existing", "revision read fails", "foreign pool", "wrong TLS"} {
		t.Run(mode, func(t *testing.T) {
			domain := model.Domain{ID: "domain", PoolID: "pool", TLSMode: model.TLSManaged, CertificateID: "existing"}
			cert := model.Certificate{ID: "new", PoolID: "pool"}
			if mode == "foreign pool" {
				cert.PoolID = "other"
			}
			if mode == "wrong TLS" {
				domain.TLSMode = model.TLSCustom
			}
			script := &queryScript{t: t, transactions: true, replies: []*queryReply{boundaryDomainRead(t, domain).query}}
			failure := errors.New("desired revision unavailable")
			if mode == "existing" || mode == "revision read fails" {
				q := boundaryRead("SELECT desired_revision FROM gateway_pools", []string{"revision"}, int64(7)).query
				if mode == "revision read fails" {
					q.err = failure
				}
				script.replies = append(script.replies, q)
			}
			s := storeWithScript(t, script)
			revision, e := s.ProvisionManagedCertificate(context.Background(), &cert, domain.ID, time.Now())
			if mode == "existing" {
				if e != nil || revision != 7 {
					t.Fatal("existing certificate selection changed", revision, e)
				}
			} else {
				want := ErrConflict
				if mode == "revision read fails" {
					want = failure
				}
				if !errors.Is(e, want) || revision != 0 {
					t.Fatal("unknown provision reported as confirmed", revision, e)
				}
			}
			if script.execCalls != 0 || script.commits != 0 || script.rollbacks != 1 {
				t.Fatal("existing selection replaced")
			}
		})
	}
}
func TestCertificateActivationRejectsStaleOrForeignOwnership(t *testing.T) {
	for _, mode := range []string{"stale revision", "foreign pool", "inactive candidate", "same content", "new key", "new PEM", "write race"} {
		t.Run(mode, func(t *testing.T) {
			old := model.Certificate{ID: "cert", PoolID: "pool", Status: model.CertificateActive, CertificatePEM: "pem", EncryptedPrivateKey: []byte("key")}
			next := old
			expected := int64(1)
			switch mode {
			case "stale revision":
				expected = 2
			case "foreign pool":
				next.PoolID = "foreign"
			case "inactive candidate":
				next.Status = model.CertificatePending
			case "new key":
				next.EncryptedPrivateKey = []byte("new key")
			case "new PEM":
				next.CertificatePEM = "new PEM"
			}
			steps := []transactionBoundaryStep{boundaryRead("SELECT pool_id,status,certificate_pem,encrypted_private_key,revision FROM certificates", []string{"pool", "status", "pem", "key", "revision"}, "pool", string(old.Status), old.CertificatePEM, old.EncryptedPrivateKey, int64(1))}
			valid := mode == "same content" || mode == "new key" || mode == "new PEM"
			if valid || mode == "write race" {
				steps = append(steps, boundaryWrite("UPDATE certificates SET provider"))
				if mode == "write race" {
					steps[len(steps)-1].write.affected = 0
				} else {
					steps = append(steps, boundaryWrite("UPDATE domains SET status"))
					if mode != "same content" {
						steps = append(steps, boundaryPoolSteps(SQLite)...)
					}
				}
			}
			script := &queryScript{t: t, transactions: true}
			for _, step := range steps {
				if step.query != nil {
					script.replies = append(script.replies, step.query)
				} else {
					script.execs = append(script.execs, step.write)
				}
			}
			s := storeWithScript(t, script)
			e := s.ActivateCertificate(context.Background(), &next, expected, time.Now())
			if valid {
				if e != nil || script.commits != 1 {
					t.Fatal("verified activation not committed", e)
				}
			} else {
				want := ErrConflict
				if mode == "stale revision" || mode == "write race" {
					want = ErrPrecondition
				}
				if !errors.Is(e, want) || script.commits != 0 || script.rollbacks != 1 {
					t.Fatal("unsafe activation committed", e)
				}
			}
		})
	}
}
