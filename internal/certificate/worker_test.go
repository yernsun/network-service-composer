package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func certificateTestStore(t *testing.T) *storage.SQLStore {
	t.Helper()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "certificates.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestValidateIssuedCertificate(t *testing.T) {
	certificatePEM, keyPEM := selfSignedCertificate(t, "api.example.com", time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	leaf, err := validateIssued(certificatePEM, keyPEM, []string{"api.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if leaf.VerifyHostname("api.example.com") != nil {
		t.Fatal("validated leaf does not cover requested hostname")
	}
	if _, err = validateIssued(certificatePEM, keyPEM, []string{"other.example.com"}); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("hostname mismatch returned %v", err)
	}
	expiredPEM, expiredKey := selfSignedCertificate(t, "api.example.com", time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	if _, err = validateIssued(expiredPEM, expiredKey, []string{"api.example.com"}); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired certificate returned %v", err)
	}
}

func selfSignedCertificate(t *testing.T, host string, notBefore, notAfter time.Time) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: host},
		DNSNames:              []string{host},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func TestPersistentHTTPChallengeLifecycle(t *testing.T) {
	ctx := context.Background()
	store := certificateTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "challenge-worker", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificatePending, PrimaryDomain: "challenge.example.com", Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	provider := httpChallengeProvider{store: store, certificateID: certificate.ID}
	if err := provider.Present("challenge.example.com", "token-value", "key-authorization"); err != nil {
		t.Fatal(err)
	}
	challenge, err := store.GetChallenge(ctx, "token-value", time.Now().UTC())
	if err != nil || challenge.KeyAuthorization != "key-authorization" || challenge.CertificateID != certificate.ID {
		t.Fatalf("unexpected persisted challenge: %#v, err=%v", challenge, err)
	}
	if err = provider.CleanUp("challenge.example.com", "token-value", "key-authorization"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetChallenge(ctx, "token-value", time.Now().UTC()); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("cleaned challenge returned %v", err)
	}
}

func TestDNSProviderConstructors(t *testing.T) {
	tests := map[string]config.DNSProviderProfile{
		"alidns":       {Provider: "alidns", Credentials: config.DNSProviderCredentials{AccessKey: "access", SecretKey: "secret"}},
		"cloudflare":   {Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "token"}},
		"tencentcloud": {Provider: "tencentcloud", Credentials: config.DNSProviderCredentials{SecretID: "id", SecretKey: "secret"}},
		"hurricane":    {Provider: "hurricane", Credentials: config.DNSProviderCredentials{Tokens: map[string]string{"api.example.com": "token"}}},
	}
	for name, profile := range tests {
		t.Run(name, func(t *testing.T) {
			provider, err := newDNSProvider(profile)
			if err != nil {
				t.Fatal(err)
			}
			if provider == nil {
				t.Fatal("provider constructor returned nil")
			}
		})
	}
}

func TestCertificateChallengeValidation(t *testing.T) {
	if challenge, err := certificateChallenge(model.Certificate{}); err != nil || challenge != model.ACMEHTTP01 {
		t.Fatalf("legacy certificate challenge = %q, err=%v", challenge, err)
	}
	if challenge, err := certificateChallenge(model.Certificate{ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "dns"}); err != nil || challenge != model.ACMEDNS01 {
		t.Fatalf("DNS certificate challenge = %q, err=%v", challenge, err)
	}
	if _, err := certificateChallenge(model.Certificate{ACMEChallenge: model.ACMEDNS01}); err == nil {
		t.Fatal("DNS-01 certificate without a provider reference was accepted")
	}
}

func TestDNS01DoesNotWaitForGatewayACK(t *testing.T) {
	ctx := context.Background()
	store := certificateTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "dns-no-gateway", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "dns.example.com", TLSMode: model.TLSManaged, ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "cloudflare", HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", ACMEChallenge: model.ACMEDNS01, DNSProviderRef: "cloudflare", Status: model.CertificatePending, PrimaryDomain: domain.Host, Domains: []string{domain.Host}, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateManagedDomain(ctx, &domain, &certificate); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.ACME.DNSProviders["cloudflare"] = config.DNSProviderProfile{Provider: "cloudflare", Credentials: config.DNSProviderCredentials{APIToken: "token"}}
	worker := NewWorker(store, lease.New(store.DB(), store.Dialect()), nil, cfg, nil, nil)
	if err := worker.process(ctx, certificate.ID); err == nil || !strings.Contains(err.Error(), "terms") {
		t.Fatalf("DNS-01 process should advance past Gateway ACK and fail at disabled ACME terms, got %v", err)
	}
	updated, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(updated.LastError, "gateway ACK") || updated.AttemptCount != 1 {
		t.Fatalf("DNS-01 incorrectly waited for Gateway ACK: %#v", updated)
	}
}

func TestScheduleRenewalsUsesPersistentRetryState(t *testing.T) {
	ctx := context.Background()
	store := certificateTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "renew-worker", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	notBefore := now.Add(-80 * 24 * time.Hour)
	notAfter := now.Add(10 * 24 * time.Hour)
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificateActive, PrimaryDomain: "renew.example.com", CertificatePEM: "old-certificate", EncryptedPrivateKey: []byte("old-key"), NotBefore: &notBefore, NotAfter: &notAfter, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Controller.Certificate.RenewBeforeExpiry = config.Duration(30 * 24 * time.Hour)
	worker := NewWorker(store, lease.New(store.DB(), store.Dialect()), nil, cfg, nil, nil)
	if err := worker.scheduleRenewals(ctx, now); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.CertificateRenewing || updated.NextAttemptAt == nil || !updated.NextAttemptAt.Equal(now) {
		t.Fatalf("certificate was not scheduled for renewal: %#v", updated)
	}
}

func TestScheduleRenewalsCapsWindowForShortLivedCertificate(t *testing.T) {
	ctx := context.Background()
	store := certificateTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "short-renew-worker", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(6 * 24 * time.Hour)
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "acme", Status: model.CertificateActive, PrimaryDomain: "short.example.com", CertificatePEM: "short-certificate", EncryptedPrivateKey: []byte("short-key"), NotBefore: &notBefore, NotAfter: &notAfter, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Controller.Certificate.RenewBeforeExpiry = config.Duration(30 * 24 * time.Hour)
	worker := NewWorker(store, lease.New(store.DB(), store.Dialect()), nil, cfg, nil, nil)
	if err := worker.scheduleRenewals(ctx, now); err != nil {
		t.Fatal(err)
	}
	unchanged, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Status != model.CertificateActive || unchanged.NextAttemptAt != nil {
		t.Fatalf("short-lived certificate renewed immediately: %#v", unchanged)
	}
	due := certificateRenewalTime(certificate, 30*24*time.Hour)
	if due.Before(now.Add(3*24*time.Hour)) || due.After(now.Add(5*24*time.Hour)) {
		t.Fatalf("short-lived certificate renewal time = %v", due)
	}
	if err = worker.scheduleRenewals(ctx, due); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetCertificate(ctx, certificate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.CertificateRenewing || updated.NextAttemptAt == nil || !updated.NextAttemptAt.Equal(due) {
		t.Fatalf("short-lived certificate was not scheduled at bounded window: %#v", updated)
	}
}

func TestManagedCertificateRecoveryRepairsUnlinkedDomain(t *testing.T) {
	ctx := context.Background()
	store := certificateTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "managed-recovery", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "recovery.example.com", TLSMode: model.TLSManaged, HTTPSRedirect: true, Status: model.DomainCertPending, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(store, lease.New(store.DB(), store.Dialect()), nil, config.Defaults(), nil, nil)
	if err := worker.ensureManagedCertificates(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetDomain(ctx, domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CertificateID == "" || updated.Status != model.DomainCertPending {
		t.Fatalf("managed domain was not repaired: %#v", updated)
	}
	certificate, err := store.GetCertificate(ctx, updated.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if certificate.Provider != "acme" || certificate.Status != model.CertificatePending || certificate.PrimaryDomain != domain.Host || certificate.DistributionRevision == 0 {
		t.Fatalf("unexpected recovered certificate: %#v", certificate)
	}
	if err := worker.ensureManagedCertificates(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	certificates, err := store.ListCertificates(ctx, storage.Page{Limit: 10})
	if err != nil || len(certificates) != 1 {
		t.Fatalf("recovery was not idempotent: certificates=%#v err=%v", certificates, err)
	}
}

func TestRetryBackoffAndJitterAreBounded(t *testing.T) {
	if got := retryDelay(0); got != 5*time.Minute {
		t.Fatalf("initial retry = %v", got)
	}
	if got := retryDelay(100); got != 24*time.Hour {
		t.Fatalf("maximum retry = %v", got)
	}
	window := 72 * time.Hour
	first := deterministicJitter("cert_01", window)
	if first < 0 || first >= window || deterministicJitter("cert_01", window) != first {
		t.Fatalf("jitter is not deterministic and bounded: %v", first)
	}
	if got := deterministicJitter("cert_01", 0); got != 0 {
		t.Fatalf("zero-window jitter = %v", got)
	}
}

type testLease struct {
	lost   chan struct{}
	checks atomic.Int64
}

func (l *testLease) Check(context.Context) error {
	l.checks.Add(1)
	select {
	case <-l.lost:
		return lease.ErrLost
	default:
		return nil
	}
}

func (*testLease) Release(context.Context) error { return nil }

func TestRunWithLeaseCancelsWorkWhenOwnershipIsLost(t *testing.T) {
	ownership := &testLease{lost: make(chan struct{})}
	started := make(chan struct{})
	canceled := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runWithLease(context.Background(), ownership, 5*time.Millisecond, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(canceled)
			return ctx.Err()
		})
	}()
	<-started
	close(ownership.lost)
	select {
	case err := <-done:
		if !errors.Is(err, lease.ErrLost) {
			t.Fatalf("runWithLease() = %v, want ErrLost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lease loss did not stop work")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("lease loss did not cancel the work context")
	}
	if ownership.checks.Load() < 2 {
		t.Fatalf("lease was checked only %d time(s)", ownership.checks.Load())
	}
}
