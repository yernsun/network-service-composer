package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controller"
	"github.com/yernsun/network-service-composer/internal/controlplane"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("controller stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return usage()
	}
	switch os.Args[1] {
	case "serve":
		return serve(os.Args[2:])
	case "migrate":
		return migrate(os.Args[2:])
	case "recovery-check":
		return recoveryCheck(os.Args[2:])
	case "recovery-prepare":
		return recoveryPrepare(os.Args[2:])
	case "recovery-release":
		return recoveryRelease(os.Args[2:])
	case "recovery-reconcile":
		return recoveryReconcile(os.Args[2:])
	case "keys":
		return keys(os.Args[2:])
	case "pki":
		return runPKI(os.Args[2:])
	case "id":
		return generateID(os.Args[2:])
	case "version":
		fmt.Println(version)
		return nil
	default:
		return usage()
	}
}

func usage() error {
	return fmt.Errorf("usage: nsc-controller <serve|migrate|recovery-check|recovery-prepare|recovery-release|recovery-reconcile|keys|pki|id|version>")
}

func generateID(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: nsc-controller id <pool|runtime|service|endpoint|domain|route|certificate|gateway>")
	}
	kinds := map[string]id.Kind{"pool": id.Pool, "runtime": id.Runtime, "service": id.Service, "endpoint": id.Endpoint, "domain": id.Domain, "route": id.Route, "certificate": id.Certificate, "gateway": id.Gateway}
	kind, ok := kinds[args[0]]
	if !ok {
		return fmt.Errorf("unsupported ID kind %q", args[0])
	}
	fmt.Println(id.New(kind))
	return nil
}

func load(args []string, name string) (config.Config, error) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	path := set.String("config", "/etc/nsc/config.yaml", "configuration file")
	if err := set.Parse(args); err != nil {
		return config.Config{}, err
	}
	return config.Load(*path)
}

func serve(args []string) error {
	cfg, err := load(args, "serve")
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	app, err := controller.New(ctx, cfg, logger)
	if err != nil {
		return err
	}
	return app.Run(ctx)
}

func migrate(args []string) error {
	cfg, err := load(args, "migrate")
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	return store.Migrate(context.Background())
}

func keys(args []string) error {
	if len(args) == 0 || args[0] != "reencrypt" {
		return fmt.Errorf("usage: nsc-controller keys reencrypt --config PATH")
	}
	cfg, err := load(args[1:], "keys reencrypt")
	if err != nil {
		return err
	}
	store, err := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	ring, err := security.NewKeyring(cfg.Security.PrimaryKeyID, cfg.Security.EncryptionKeys)
	if err != nil {
		return err
	}
	certificateCount, err := store.ReencryptCertificates(context.Background(), func(certificate model.Certificate) ([]byte, string, error) {
		plaintext, _, err := ring.Decrypt(certificate.EncryptedPrivateKey, []byte(certificate.ID))
		if err != nil {
			return nil, "", err
		}
		encrypted, err := ring.Encrypt(plaintext, []byte(certificate.ID))
		return encrypted, ring.PrimaryID(), err
	})
	if err != nil {
		return err
	}
	accountCount, err := store.ReencryptACMEAccounts(context.Background(), func(account model.ACMEAccount) ([]byte, string, error) {
		plaintext, _, decryptErr := ring.Decrypt(account.EncryptedPrivateKey, []byte(account.ID))
		if decryptErr != nil {
			return nil, "", decryptErr
		}
		encrypted, encryptErr := ring.Encrypt(plaintext, []byte(account.ID))
		return encrypted, ring.PrimaryID(), encryptErr
	})
	if err != nil {
		return err
	}
	revisionCount, err := store.ReencryptApplicationRevisions(context.Background(), func(revision model.ApplicationRevision) ([]byte, []byte, []byte, string, error) {
		compose, _, decryptErr := ring.Decrypt(revision.EncryptedCompose, []byte("application-revision:"+revision.ID+":compose"))
		if decryptErr != nil {
			return nil, nil, nil, "", decryptErr
		}
		environment, _, decryptErr := ring.Decrypt(revision.EncryptedEnvironment, []byte("application-revision:"+revision.ID+":environment"))
		if decryptErr != nil {
			return nil, nil, nil, "", decryptErr
		}
		resolved, _, decryptErr := ring.Decrypt(revision.EncryptedResolved, []byte("application-revision:"+revision.ID+":resolved"))
		if decryptErr != nil {
			return nil, nil, nil, "", decryptErr
		}
		composeEnvelope, encryptErr := ring.Encrypt(compose, []byte("application-revision:"+revision.ID+":compose"))
		if encryptErr != nil {
			return nil, nil, nil, "", encryptErr
		}
		environmentEnvelope, encryptErr := ring.Encrypt(environment, []byte("application-revision:"+revision.ID+":environment"))
		if encryptErr != nil {
			return nil, nil, nil, "", encryptErr
		}
		resolvedEnvelope, encryptErr := ring.Encrypt(resolved, []byte("application-revision:"+revision.ID+":resolved"))
		return composeEnvelope, environmentEnvelope, resolvedEnvelope, ring.PrimaryID(), encryptErr
	})
	if err != nil {
		return err
	}
	operationCount, err := store.ReencryptDeploymentOperations(context.Background(), func(operation model.DeploymentOperation) ([]byte, string, error) {
		plaintext, _, decryptErr := ring.Decrypt(operation.EncryptedInput, []byte("deployment-operation:"+operation.ID))
		if decryptErr != nil {
			return nil, "", decryptErr
		}
		encrypted, encryptErr := ring.Encrypt(plaintext, []byte("deployment-operation:"+operation.ID))
		return encrypted, ring.PrimaryID(), encryptErr
	})
	if err != nil {
		return err
	}
	controlCount, err := core.New(store, cfg, ring).ReencryptControlSecrets(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("re-encrypted %d control-plane secret documents\n", controlCount)
	fmt.Printf("re-encrypted %d certificate private keys, %d ACME account keys, %d application revisions, and %d deployment operation inputs\n", certificateCount, accountCount, revisionCount, operationCount)
	return nil
}

type stringList []string

func (s *stringList) String() string { return fmt.Sprint([]string(*s)) }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runPKI(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: nsc-controller pki <init-ca|issue-controller|issue-agent>")
	}
	switch args[0] {
	case "init-ca":
		set := flag.NewFlagSet("pki init-ca", flag.ContinueOnError)
		certPath := set.String("cert", "ca.crt", "output CA certificate")
		keyPath := set.String("key", "ca.key", "output CA private key")
		commonName := set.String("common-name", "Network Service Composer Offline CA", "CA common name")
		validFor := set.Duration("valid-for", 10*365*24*60*time.Minute, "CA validity")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		return pki.InitCA(*certPath, *keyPath, *commonName, *validFor)
	case "issue-controller":
		set := flag.NewFlagSet("pki issue-controller", flag.ContinueOnError)
		caCert := set.String("ca-cert", "ca.crt", "CA certificate")
		caKey := set.String("ca-key", "ca.key", "CA private key")
		certPath := set.String("cert", "controller.crt", "output certificate")
		keyPath := set.String("key", "controller.key", "output private key")
		commonName := set.String("common-name", "nsc-controller", "controller common name")
		instanceID := set.String("instance-id", "", "instance identity for internal mutual TLS")
		validFor := set.Duration("valid-for", 365*24*time.Hour, "certificate validity")
		var dnsNames, rawIPs stringList
		set.Var(&dnsNames, "dns", "DNS SAN (repeatable)")
		set.Var(&rawIPs, "ip", "IP SAN (repeatable)")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if len(dnsNames) == 0 && len(rawIPs) == 0 {
			dnsNames = append(dnsNames, *commonName)
		}
		var addresses []net.IP
		for _, value := range rawIPs {
			address := net.ParseIP(value)
			if address == nil {
				return fmt.Errorf("invalid IP SAN %q", value)
			}
			addresses = append(addresses, address)
		}
		uri, err := controlplane.IdentityURI(*instanceID)
		if err != nil {
			return fmt.Errorf("--instance-id is required: %w", err)
		}
		return pki.Issue(*caCert, *caKey, *certPath, *keyPath, pki.CertificateOptions{CommonName: *commonName, DNSNames: dnsNames, IPAddresses: addresses, URIs: []*url.URL{uri}, Duration: *validFor, Server: true, Client: true})
	case "issue-agent":
		set := flag.NewFlagSet("pki issue-agent", flag.ContinueOnError)
		caCert := set.String("ca-cert", "ca.crt", "CA certificate")
		caKey := set.String("ca-key", "ca.key", "CA private key")
		certPath := set.String("cert", "agent.crt", "output certificate")
		keyPath := set.String("key", "agent.key", "output private key")
		poolID := set.String("pool-id", "", "bound gateway pool ID")
		gatewayID := set.String("gateway-id", "", "bound gateway ID")
		validFor := set.Duration("valid-for", 365*24*time.Hour, "certificate validity")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		identity, err := pki.GatewayIdentityURI(*poolID, *gatewayID)
		if err != nil {
			return err
		}
		return pki.Issue(*caCert, *caKey, *certPath, *keyPath, pki.CertificateOptions{CommonName: *gatewayID, URIs: []*url.URL{identity}, Duration: *validFor, Client: true})
	default:
		return fmt.Errorf("usage: nsc-controller pki <init-ca|issue-controller|issue-agent>")
	}
}
