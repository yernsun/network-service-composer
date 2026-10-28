package certificate

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	legocert "github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/dnsprofile"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/lease"
	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type Worker struct {
	store   storage.Store
	locker  lease.Locker
	keyring *security.Keyring
	config  config.Config
	metrics *metricsx.Metrics
	logger  *slog.Logger
}

func NewWorker(store storage.Store, locker lease.Locker, keyring *security.Keyring, cfg config.Config, metrics *metricsx.Metrics, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{store: store, locker: locker, keyring: keyring, config: cfg, metrics: metrics, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	maintenanceInterval := w.config.Controller.Certificate.ReconcileInterval.Std()
	if maintenanceInterval <= 0 {
		maintenanceInterval = time.Hour
	}
	if err := w.reconcile(ctx); err != nil {
		w.logger.Error("certificate reconciliation failed", "error", err)
	}
	jobPollInterval := min(5*time.Second, maintenanceInterval)
	ticker := time.NewTicker(jobPollInterval)
	defer ticker.Stop()
	nextMaintenance := time.Now().Add(maintenanceInterval)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if !now.Before(nextMaintenance) {
				if err := w.maintain(ctx, now.UTC()); err != nil {
					w.logger.Error("certificate maintenance failed", "error", err)
				}
				nextMaintenance = now.Add(maintenanceInterval)
			}
			if err := w.processPending(ctx, now.UTC()); err != nil {
				w.logger.Error("certificate job polling failed", "error", err)
			}
		}
	}
	return ctx.Err()
}

func (w *Worker) reconcile(ctx context.Context) error {
	if w.config.Controller.RecoveryMode {
		return nil
	}
	now := time.Now().UTC()
	if err := w.maintain(ctx, now); err != nil {
		return err
	}
	return w.processPending(ctx, now)
}

func (w *Worker) maintain(ctx context.Context, now time.Time) error {
	if held, err := storage.RecoveryHeld(ctx, w.store, "maintenance", "certificates"); err != nil || held {
		return err
	}
	if err := w.garbageCollect(ctx, now); err != nil {
		return err
	}
	return w.scheduleRenewals(ctx, now)
}

func (w *Worker) processPending(ctx context.Context, now time.Time) error {
	if err := w.ensureManagedCertificates(ctx, now); err != nil {
		return err
	}
	values, err := w.store.ListPendingCertificates(ctx, now, 50)
	if err != nil {
		return err
	}
	for _, value := range values {
		if value.Provider != "acme" {
			continue
		}
		ownership, lockErr := w.locker.TryLock(ctx, "certificate/"+value.ID)
		if errors.Is(lockErr, lease.ErrBusy) {
			continue
		}
		if lockErr != nil {
			return lockErr
		}
		processErr := runWithLease(ctx, ownership, 2*time.Second, func(ownedCtx context.Context) error {
			return w.process(ownedCtx, value.ID)
		})
		_ = ownership.Release(context.Background())
		if processErr != nil && !errors.Is(processErr, context.Canceled) {
			w.logger.Warn("certificate job failed", "certificate_id", value.ID, "error", processErr)
		}
	}
	return nil
}

// ensureManagedCertificates repairs the narrow crash window between changing
// a domain to managed TLS and enqueueing its ACME certificate. Provisioning is
// idempotent in the Store and fenced by a per-domain distributed lock, so every
// active Controller may run this recovery pass.
func (w *Worker) ensureManagedCertificates(ctx context.Context, now time.Time) error {
	domains, err := listDomains(ctx, w.store)
	if err != nil {
		return err
	}
	for _, candidate := range domains {
		if !candidate.Enabled || candidate.TLSMode != model.TLSManaged || candidate.CertificateID != "" {
			continue
		}
		if held, err := storage.RecoveryHeld(ctx, w.store, "domain-certificate", candidate.ID); err != nil {
			return err
		} else if held {
			continue
		}
		ownership, lockErr := w.locker.TryLock(ctx, "managed-domain/"+candidate.ID)
		if errors.Is(lockErr, lease.ErrBusy) {
			continue
		}
		if lockErr != nil {
			return lockErr
		}
		provisionErr := runWithLease(ctx, ownership, 2*time.Second, func(ownedCtx context.Context) error {
			current, getErr := w.store.GetDomain(ownedCtx, candidate.ID)
			if getErr != nil {
				return getErr
			}
			if !current.Enabled || current.TLSMode != model.TLSManaged || current.CertificateID != "" {
				return nil
			}
			certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: current.PoolID, Provider: "acme",
				ACMEChallenge: current.ACMEChallenge, DNSProviderRef: current.DNSProviderRef,
				Status: model.CertificatePending, PrimaryDomain: current.Host, Domains: []string{current.Host}, Revision: 1,
				CreatedAt: now, UpdatedAt: now}
			_, provisionErr := w.store.ProvisionManagedCertificate(ownedCtx, &certificate, current.ID, now)
			return provisionErr
		})
		_ = ownership.Release(context.Background())
		if provisionErr != nil && !errors.Is(provisionErr, context.Canceled) {
			return provisionErr
		}
	}
	return nil
}

func runWithLease(ctx context.Context, ownership lease.Lease, interval time.Duration, work func(context.Context) error) error {
	if err := ownership.Check(ctx); err != nil {
		return err
	}
	ownedCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if interval <= 0 {
		interval = 2 * time.Second
	}
	lost := make(chan error, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ownedCtx.Done():
				return
			case <-ticker.C:
				if err := ownership.Check(ownedCtx); err != nil {
					lost <- err
					cancel()
					return
				}
			}
		}
	}()
	workErr := work(ownedCtx)
	cancel()
	<-monitorDone
	select {
	case lostErr := <-lost:
		return lostErr
	default:
		return workErr
	}
}

func (w *Worker) garbageCollect(ctx context.Context, now time.Time) error {
	ownership, err := w.locker.TryLock(ctx, "gc/global")
	if errors.Is(err, lease.ErrBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer ownership.Release(context.Background())
	var result storage.GCResult
	err = runWithLease(ctx, ownership, 2*time.Second, func(ownedCtx context.Context) error {
		var collectErr error
		result, collectErr = w.store.GarbageCollect(ownedCtx, now, w.config.Controller.GC.FailedCertificateRetention.Std())
		if collectErr != nil {
			return collectErr
		}
		return w.store.PruneRuntimeEvents(ownedCtx, now.Add(-w.config.Controller.RuntimeEvents.Retention.Std()))
	})
	if err != nil {
		return err
	}
	if result.ExpiredChallenges > 0 || result.Certificates > 0 {
		w.logger.Info("certificate garbage collection completed", "expired_challenges", result.ExpiredChallenges, "certificates", result.Certificates)
	}
	return nil
}

func (w *Worker) scheduleRenewals(ctx context.Context, now time.Time) error {
	values, err := listCertificates(ctx, w.store)
	if err != nil {
		return err
	}
	renewBefore := w.config.Controller.Certificate.RenewBeforeExpiry.Std()
	if renewBefore <= 0 {
		renewBefore = 30 * 24 * time.Hour
	}
	for _, value := range values {
		if value.Provider != "acme" || value.Status != model.CertificateActive || value.NotAfter == nil || value.NextAttemptAt != nil {
			continue
		}
		if now.Before(certificateRenewalTime(value, renewBefore)) {
			continue
		}
		if held, err := storage.RecoveryHeld(ctx, w.store, "certificate", value.ID); err != nil {
			return err
		} else if held {
			continue
		}
		value.Status = model.CertificateRenewing
		value.NextAttemptAt = &now
		value.UpdatedAt = now
		if err := w.store.UpdateCertificate(ctx, &value, value.Revision); err != nil && !errors.Is(err, storage.ErrConflict) {
			return err
		}
	}
	return nil
}

func certificateRenewalTime(value model.Certificate, renewBefore time.Duration) time.Time {
	if renewBefore <= 0 {
		renewBefore = 30 * 24 * time.Hour
	}
	// A configured lead time longer than a short-lived certificate would make
	// every newly issued certificate immediately eligible for another order.
	// Keep the usual 30-day window for 90-day certificates, while never opening
	// the renewal window earlier than the final third of the actual lifetime.
	if value.NotBefore != nil && value.NotAfter != nil {
		lifetime := value.NotAfter.Sub(*value.NotBefore)
		if maximumLead := lifetime / 3; maximumLead > 0 && renewBefore > maximumLead {
			renewBefore = maximumLead
		}
	}
	return value.NotAfter.Add(-renewBefore).Add(deterministicJitter(value.ID, renewBefore/10))
}

func (w *Worker) process(ctx context.Context, certificateID string) error {
	if held, err := storage.RecoveryHeld(ctx, w.store, "certificate", certificateID); err != nil || held {
		return err
	}
	value, err := w.store.GetCertificate(ctx, certificateID)
	if err != nil {
		return err
	}
	if value.Status == model.CertificateRevoked {
		return w.revoke(ctx, value)
	}
	challengeType, err := certificateChallenge(value)
	if err != nil {
		return w.failUnlessCanceled(ctx, value, len(value.CertificatePEM) > 0, err)
	}
	if challengeType == model.ACMEHTTP01 && len(value.CertificatePEM) == 0 && value.DistributionRevision > 0 {
		acked, ackErr := w.store.GatewayACKed(ctx, value.PoolID, value.DistributionRevision, time.Now().UTC().Add(-w.config.Controller.Gateway.OfflineTimeout.Std()))
		if ackErr != nil {
			return ackErr
		}
		if !acked {
			next := time.Now().UTC().Add(30 * time.Second)
			value.Status = model.CertificatePending
			value.NextAttemptAt = &next
			value.LastError = "waiting for a gateway ACK containing the HTTP-01 system route"
			value.UpdatedAt = time.Now().UTC()
			if err = w.store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
				return err
			}
			return nil
		}
	}
	hadCertificate := len(value.CertificatePEM) > 0
	if hadCertificate {
		value.Status = model.CertificateRenewing
	} else {
		value.Status = model.CertificateAuthorizing
	}
	value.NextAttemptAt = nil
	value.LastError = ""
	value.UpdatedAt = time.Now().UTC()
	if err = w.store.UpdateCertificate(ctx, &value, value.Revision); err != nil {
		return err
	}
	value, err = w.store.GetCertificate(ctx, certificateID)
	if err != nil {
		return err
	}
	if challengeType == model.ACMEDNS01 && !hadCertificate {
		if err = w.store.UpdateCertificateDomains(ctx, value.ID, model.DomainDNSPending, "", time.Now().UTC()); err != nil {
			return err
		}
	}
	client, err := w.client(ctx, value, true)
	if err != nil {
		return w.failUnlessCanceled(ctx, value, hadCertificate, err)
	}
	resource, err := client.Certificate.Obtain(legocert.ObtainRequest{Domains: value.Domains, Bundle: true, AlwaysDeactivateAuthorizations: true})
	if err != nil {
		return w.failUnlessCanceled(ctx, value, hadCertificate, err)
	}
	leaf, err := validateIssued(resource.Certificate, resource.PrivateKey, value.Domains)
	if err != nil {
		return w.failUnlessCanceled(ctx, value, hadCertificate, err)
	}
	encrypted, err := w.keyring.Encrypt(resource.PrivateKey, []byte(value.ID))
	if err != nil {
		return w.failUnlessCanceled(ctx, value, hadCertificate, err)
	}
	fingerprint := sha256.Sum256(leaf.Raw)
	notBefore, notAfter := leaf.NotBefore.UTC(), leaf.NotAfter.UTC()
	value.Status = model.CertificateActive
	value.CertificatePEM = string(resource.Certificate)
	value.EncryptedPrivateKey = encrypted
	value.EncryptionKeyID = w.keyring.PrimaryID()
	value.Fingerprint = hex.EncodeToString(fingerprint[:])
	value.SerialNumber = leaf.SerialNumber.String()
	value.NotBefore, value.NotAfter = &notBefore, &notAfter
	value.AttemptCount = 0
	value.NextAttemptAt = nil
	value.LastError = ""
	value.UpdatedAt = time.Now().UTC()
	if err = w.store.ActivateCertificate(ctx, &value, value.Revision, value.UpdatedAt); err != nil {
		return err
	}
	_ = w.store.DeleteChallenges(ctx, value.ID)
	if w.metrics != nil {
		w.metrics.ACMEOrders.WithLabelValues("success").Inc()
	}
	return nil
}

func (w *Worker) revoke(ctx context.Context, value model.Certificate) error {
	if len(value.CertificatePEM) == 0 {
		value.NextAttemptAt = nil
		value.LastError = ""
		value.UpdatedAt = time.Now().UTC()
		return w.store.UpdateCertificate(ctx, &value, value.Revision)
	}
	client, err := w.client(ctx, value, false)
	if err == nil {
		err = client.Certificate.Revoke([]byte(value.CertificatePEM))
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		value.AttemptCount++
		next := time.Now().UTC().Add(retryDelay(value.AttemptCount))
		value.NextAttemptAt = &next
		value.LastError = truncateError(err)
		value.UpdatedAt = time.Now().UTC()
		_ = w.store.UpdateCertificate(ctx, &value, value.Revision)
		if w.metrics != nil {
			w.metrics.ACMEErrors.WithLabelValues("revoke").Inc()
		}
		return err
	}
	value.AttemptCount = 0
	value.NextAttemptAt = nil
	value.LastError = ""
	value.UpdatedAt = time.Now().UTC()
	return w.store.UpdateCertificate(ctx, &value, value.Revision)
}

func (w *Worker) failUnlessCanceled(ctx context.Context, value model.Certificate, preserveActive bool, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return w.fail(ctx, value, preserveActive, cause)
}

func (w *Worker) fail(ctx context.Context, value model.Certificate, preserveActive bool, cause error) error {
	current, err := w.store.GetCertificate(ctx, value.ID)
	if err != nil {
		return err
	}
	current.AttemptCount++
	next := time.Now().UTC().Add(retryDelay(current.AttemptCount))
	current.NextAttemptAt = &next
	current.LastError = truncateError(cause)
	if preserveActive && len(current.CertificatePEM) > 0 {
		current.Status = model.CertificateActive
	} else {
		current.Status = model.CertificateError
		_ = w.store.UpdateCertificateDomains(ctx, current.ID, model.DomainError, current.LastError, time.Now().UTC())
	}
	current.UpdatedAt = time.Now().UTC()
	if updateErr := w.store.UpdateCertificate(ctx, &current, current.Revision); updateErr != nil {
		return fmt.Errorf("%v; persist retry: %w", cause, updateErr)
	}
	if w.metrics != nil {
		w.metrics.ACMEOrders.WithLabelValues("failure").Inc()
		w.metrics.ACMEErrors.WithLabelValues("order").Inc()
	}
	return cause
}

func (w *Worker) client(ctx context.Context, certificate model.Certificate, configureChallenge bool) (*lego.Client, error) {
	if !w.config.ACME.AcceptTerms {
		return nil, fmt.Errorf("ACME terms must be explicitly accepted")
	}
	if w.config.ACME.Email == "" || w.config.ACME.DirectoryURL == "" {
		return nil, fmt.Errorf("ACME email and directory_url are required")
	}
	accountLock, err := w.locker.TryLock(ctx, "acme-account/"+w.config.ACME.DirectoryURL+"/"+w.config.ACME.Email)
	if err != nil {
		return nil, err
	}
	defer accountLock.Release(context.Background())
	var result *lego.Client
	err = runWithLease(ctx, accountLock, 2*time.Second, func(ownedCtx context.Context) error {
		var clientErr error
		result, clientErr = w.clientWithAccountLock(ownedCtx)
		return clientErr
	})
	if err != nil || !configureChallenge {
		return result, err
	}
	challengeType, err := certificateChallenge(certificate)
	if err != nil {
		return nil, err
	}
	switch challengeType {
	case model.ACMEHTTP01:
		err = result.Challenge.SetHTTP01Provider(&httpChallengeProvider{ctx: ctx, store: w.store, certificateID: certificate.ID})
	case model.ACMEDNS01:
		profile, profileErr := dnsprofile.Resolve(ctx, w.store, w.keyring, w.config, certificate.DNSProviderRef)
		if profileErr != nil {
			return nil, fmt.Errorf("DNS provider reference %q is unavailable: %w", certificate.DNSProviderRef, profileErr)
		}
		provider, providerErr := newDNSProvider(profile)
		if providerErr != nil {
			return nil, fmt.Errorf("initialize DNS provider reference %q: %w", certificate.DNSProviderRef, providerErr)
		}
		err = result.Challenge.SetDNS01Provider(provider)
	}
	return result, err
}

func (w *Worker) clientWithAccountLock(ctx context.Context) (*lego.Client, error) {
	account, err := w.store.GetACMEAccount(ctx, w.config.ACME.DirectoryURL, w.config.ACME.Email)
	var user acmeUser
	if errors.Is(err, storage.ErrNotFound) {
		privateKey, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			return nil, keyErr
		}
		now := time.Now().UTC()
		account = model.ACMEAccount{ID: id.New(id.Account), DirectoryURL: w.config.ACME.DirectoryURL, Email: w.config.ACME.Email, EncryptionKeyID: w.keyring.PrimaryID(), CreatedAt: now, UpdatedAt: now}
		keyDER, keyErr := x509.MarshalPKCS8PrivateKey(privateKey)
		if keyErr != nil {
			return nil, keyErr
		}
		account.EncryptedPrivateKey, keyErr = w.keyring.Encrypt(keyDER, []byte(account.ID))
		if keyErr != nil {
			return nil, keyErr
		}
		user = acmeUser{email: account.Email, privateKey: privateKey}
		client, clientErr := newLegoClient(&user, w.config)
		if clientErr != nil {
			return nil, clientErr
		}
		registrationResource, registerErr := client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if registerErr != nil {
			return nil, registerErr
		}
		user.registration = registrationResource
		registrationJSON, marshalErr := json.Marshal(registrationResource)
		if marshalErr != nil {
			return nil, marshalErr
		}
		account.Registration = string(registrationJSON)
		if storeErr := w.store.UpsertACMEAccount(ctx, &account); storeErr != nil {
			return nil, storeErr
		}
		return client, nil
	}
	if err != nil {
		return nil, err
	}
	plaintext, _, err := w.keyring.Decrypt(account.EncryptedPrivateKey, []byte(account.ID))
	if err != nil {
		return nil, err
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(plaintext)
	if err != nil {
		return nil, fmt.Errorf("parse ACME account key: %w", err)
	}
	privateKey, ok := parsedKey.(crypto.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("ACME account key type is unsupported")
	}
	user = acmeUser{email: account.Email, privateKey: privateKey}
	if account.Registration != "" {
		if err = json.Unmarshal([]byte(account.Registration), &user.registration); err != nil {
			return nil, fmt.Errorf("decode ACME registration: %w", err)
		}
	}
	client, err := newLegoClient(&user, w.config)
	if err != nil {
		return nil, err
	}
	if user.registration == nil {
		user.registration, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(user.registration)
		account.Registration = string(data)
		account.UpdatedAt = time.Now().UTC()
		if err = w.store.UpsertACMEAccount(ctx, &account); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func certificateChallenge(value model.Certificate) (model.ACMEChallengeType, error) {
	challengeType := value.ACMEChallenge
	if challengeType == "" {
		challengeType = model.ACMEHTTP01
	}
	switch challengeType {
	case model.ACMEHTTP01:
		if value.DNSProviderRef != "" {
			return "", fmt.Errorf("HTTP-01 certificate cannot reference a DNS provider")
		}
	case model.ACMEDNS01:
		if value.DNSProviderRef == "" {
			return "", fmt.Errorf("DNS-01 certificate requires a DNS provider reference")
		}
	default:
		return "", fmt.Errorf("unsupported ACME challenge type %q", challengeType)
	}
	return challengeType, nil
}

type acmeUser struct {
	email        string
	registration *registration.Resource
	privateKey   crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string                        { return u.email }
func (u *acmeUser) GetRegistration() *registration.Resource { return u.registration }
func (u *acmeUser) GetPrivateKey() crypto.PrivateKey        { return u.privateKey }

func newLegoClient(user registration.User, cfg config.Config) (*lego.Client, error) {
	legoConfig := lego.NewConfig(user)
	legoConfig.CADirURL = cfg.ACME.DirectoryURL
	legoConfig.UserAgent = "network-service-composer"
	legoConfig.Certificate.KeyType = certcrypto.EC256
	legoConfig.Certificate.Timeout = 30 * time.Second
	return lego.NewClient(legoConfig)
}

type httpChallengeProvider struct {
	ctx           context.Context
	store         storage.Store
	certificateID string
}

func (p *httpChallengeProvider) Present(domain, token, keyAuthorization string) error {
	now := time.Now().UTC()
	parent := p.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return p.store.PutChallenge(ctx, storage.Challenge{ID: id.New(id.Challenge), CertificateID: p.certificateID, Domain: domain, Type: "http-01", Token: token, KeyAuthorization: keyAuthorization, Status: "PRESENTED", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now})
}

func (p *httpChallengeProvider) CleanUp(_ string, token, _ string) error {
	parent := p.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return p.store.DeleteChallengeToken(ctx, p.certificateID, token)
}

func (p *httpChallengeProvider) Timeout() (time.Duration, time.Duration) {
	return 2 * time.Minute, 2 * time.Second
}

func validateIssued(certificatePEM, privateKeyPEM []byte, domains []string) (*x509.Certificate, error) {
	pair, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("ACME certificate and private key are invalid: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return nil, fmt.Errorf("ACME response contains no leaf certificate")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse ACME leaf certificate: %w", err)
	}
	now := time.Now().UTC()
	if certificate.IsCA {
		return nil, fmt.Errorf("ACME returned a CA certificate as the leaf")
	}
	if !allowsServerAuthentication(certificate) {
		return nil, fmt.Errorf("ACME leaf certificate does not allow TLS server authentication")
	}
	if now.Before(certificate.NotBefore) {
		return nil, fmt.Errorf("ACME returned a certificate that is not valid yet")
	}
	if now.After(certificate.NotAfter) {
		return nil, fmt.Errorf("ACME returned an expired certificate")
	}
	for _, domain := range domains {
		if err = certificate.VerifyHostname(domain); err != nil {
			return nil, fmt.Errorf("issued certificate does not cover %s: %w", domain, err)
		}
	}
	previous := certificate
	for index, der := range pair.Certificate[1:] {
		issuer, parseErr := x509.ParseCertificate(der)
		if parseErr != nil {
			return nil, fmt.Errorf("parse ACME certificate chain: %w", parseErr)
		}
		if !issuer.IsCA || !issuer.BasicConstraintsValid {
			return nil, fmt.Errorf("ACME chain issuer %d is not a CA", index+1)
		}
		if now.Before(issuer.NotBefore) || now.After(issuer.NotAfter) {
			return nil, fmt.Errorf("ACME chain issuer %d is outside its validity period", index+1)
		}
		if issuer.KeyUsage != 0 && issuer.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, fmt.Errorf("ACME chain issuer %d cannot sign certificates", index+1)
		}
		if signatureErr := previous.CheckSignatureFrom(issuer); signatureErr != nil {
			return nil, fmt.Errorf("ACME certificate chain signature %d is invalid: %w", index, signatureErr)
		}
		previous = issuer
	}
	return certificate, nil
}

func allowsServerAuthentication(certificate *x509.Certificate) bool {
	if len(certificate.ExtKeyUsage) == 0 {
		return true
	}
	for _, usage := range certificate.ExtKeyUsage {
		if usage == x509.ExtKeyUsageAny || usage == x509.ExtKeyUsageServerAuth {
			return true
		}
	}
	return false
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 5 * time.Minute
	for i := 1; i < attempt && delay < 24*time.Hour; i++ {
		delay *= 2
	}
	return min(delay, 24*time.Hour)
}

func deterministicJitter(value string, window time.Duration) time.Duration {
	if window <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(value))
	return time.Duration(binary.BigEndian.Uint64(sum[:8]) % uint64(window))
}

func truncateError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}

func listCertificates(ctx context.Context, store storage.Store) ([]model.Certificate, error) {
	var values []model.Certificate
	after := ""
	for {
		page, err := store.ListCertificates(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		values = append(values, page...)
		if len(page) < 200 {
			return values, nil
		}
		after = page[len(page)-1].ID
	}
}

func listDomains(ctx context.Context, store storage.Store) ([]model.Domain, error) {
	var values []model.Domain
	after := ""
	for {
		page, err := store.ListDomains(ctx, storage.Page{After: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		values = append(values, page...)
		if len(page) < 200 {
			return values, nil
		}
		after = page[len(page)-1].ID
	}
}
