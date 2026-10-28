package main

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"gopkg.in/yaml.v3"
)

func commandConfigFixture(t *testing.T) (config.Config, string) {
	t.Helper()
	cfg := config.Defaults()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.DSN = "file:" + filepath.Join(t.TempDir(), "controller.db") + "?_pragma=foreign_keys(1)&_time_format=sqlite"
	cfg.Security.AdminToken = "isolated-command-password"
	cfg.Security.SessionKey = strings.Repeat("s", 32)
	cfg.Security.PrimaryKeyID = "new"
	cfg.Security.EncryptionKeys = map[string]string{"old": base64.StdEncoding.EncodeToString(make([]byte, 32)), "new": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))}
	cfg.Server.GRPCListen = ""
	cfg.Server.HTTPListen = "127.0.0.1:0"
	cfg.Metrics.Listen = "127.0.0.1:0"
	cfg.Management.Enabled = true
	return cfg, filepath.Join(t.TempDir(), "config.yaml")
}
func writeCommandConfig(t *testing.T, cfg config.Config, path string) {
	t.Helper()
	b, e := yaml.Marshal(map[string]any{"storage": cfg.Storage, "security": cfg.Security, "controller": map[string]any{"recovery_mode": cfg.Controller.RecoveryMode}, "server": map[string]any{"http_listen": cfg.Server.HTTPListen, "grpc_listen": cfg.Server.GRPCListen}, "metrics": cfg.Metrics, "management": map[string]any{"enabled": cfg.Management.Enabled}})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func commandOutput(t *testing.T, args ...string) (string, error) {
	t.Helper()
	output, e := os.CreateTemp(t.TempDir(), "stdout")
	if e != nil {
		t.Fatal(e)
	}
	oldArgs, oldOutput := os.Args, os.Stdout
	os.Args = append([]string{"nsc-controller"}, args...)
	os.Stdout = output
	var err error
	func() { defer func() { os.Args = oldArgs; os.Stdout = oldOutput }(); err = run() }()
	if e = output.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(output.Name())
	if e != nil {
		t.Fatal(e)
	}
	return string(b), err
}

func TestControllerCommandsRejectInvalidInputWithoutStartingServices(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"id"}, {"id", "wrong"}, {"keys"}, {"keys", "unknown"}, {"keys", "reencrypt", "--invalid"}, {"migrate", "--invalid"}, {"serve", "--invalid"}, {"pki"}, {"pki", "unknown"}, {"pki", "init-ca", "--invalid"}, {"pki", "issue-controller", "--invalid"}, {"pki", "issue-agent", "--invalid"}, {"pki", "issue-agent"}, {"pki", "issue-controller", "--ip", "not-an-address"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, e := commandOutput(t, args...); e == nil {
				t.Fatal("invalid command accepted", args)
			}
		})
	}
	output, e := commandOutput(t, "version")
	if e != nil || strings.TrimSpace(output) != version {
		t.Fatal("version command changed product version", output, e)
	}
	for _, kind := range []string{"pool", "runtime", "service", "endpoint", "domain", "route", "certificate", "gateway"} {
		output, e := commandOutput(t, "id", kind)
		if e != nil || strings.TrimSpace(output) == "" || strings.Contains(output, " ") {
			t.Fatal("resource ID command failed", kind, output, e)
		}
	}
	cfg, path := commandConfigFixture(t)
	cfg.Storage.Driver = "mysql"
	cfg.Storage.DSN = "user@tcp("
	writeCommandConfig(t, cfg, path)
	for _, command := range []string{"migrate", "keys"} {
		args := []string{command}
		if command == "keys" {
			args = append(args, "reencrypt")
		}
		args = append(args, "--config", path)
		if _, e := commandOutput(t, args...); e == nil {
			t.Fatal("invalid storage configuration accepted", command)
		}
	}
	cfg, path = commandConfigFixture(t)
	cfg.Storage.DSN = filepath.Join(t.TempDir(), "missing", "db")
	writeCommandConfig(t, cfg, path)
	if _, e := commandOutput(t, "serve", "--config", path); e == nil {
		t.Fatal("controller started without usable storage")
	}
	cfg, path = commandConfigFixture(t)
	cfg.Metrics.Listen = "invalid listener address"
	writeCommandConfig(t, cfg, path)
	if _, e := commandOutput(t, "serve", "--config", path); e == nil {
		t.Fatal("failed server startup reported success")
	}
}

func TestControllerPKICommandsBindGatewayIdentityAndSANs(t *testing.T) {
	dir := t.TempDir()
	ca, key := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if _, e := commandOutput(t, "pki", "init-ca", "--cert", ca, "--key", key, "--common-name", "isolated CA", "--valid-for", "24h"); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"default controller", "explicit controller", "agent"} {
		t.Run(mode, func(t *testing.T) {
			cert, keyOut := filepath.Join(t.TempDir(), "cert"), filepath.Join(t.TempDir(), "key")
			sub := "issue-controller"
			args := []string{}
			pool, gateway := id.New(id.Pool), id.New(id.Gateway)
			if mode == "agent" {
				sub = "issue-agent"
				args = []string{"--pool-id", pool, "--gateway-id", gateway}
			} else if mode == "explicit controller" {
				args = []string{"--dns", "controller.test", "--dns", "second.test", "--ip", "127.0.0.1", "--ip", "::1"}
			}
			if mode != "agent" {
				args = append(args, "--instance-id", "controller")
			}
			args = append([]string{"pki", sub, "--ca-cert", ca, "--ca-key", key, "--cert", cert, "--key", keyOut, "--valid-for", "1h"}, args...)
			if _, e := commandOutput(t, args...); e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(cert)
			if e != nil {
				t.Fatal(e)
			}
			block, _ := pem.Decode(b)
			if block == nil {
				t.Fatal("certificate PEM absent")
			}
			parsed, e := x509.ParseCertificate(block.Bytes)
			if e != nil {
				t.Fatal(e)
			}
			if mode != "agent" && (len(parsed.URIs) != 1 || parsed.URIs[0].String() != "spiffe://nsc.local/controllers/controller" || len(parsed.ExtKeyUsage) != 2) {
				t.Fatal("controller lacks instance identity or mutual TLS purpose")
			}
			if mode == "agent" {
				if len(parsed.URIs) != 1 || !strings.Contains(parsed.URIs[0].String(), pool) || !strings.Contains(parsed.URIs[0].String(), gateway) || parsed.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
					t.Fatal("agent certificate lost management scope", parsed)
				}
			} else if mode == "default controller" {
				if len(parsed.DNSNames) != 1 || parsed.DNSNames[0] != "nsc-controller" {
					t.Fatal("controller default SAN missing", parsed.DNSNames)
				}
			} else if len(parsed.DNSNames) != 2 || len(parsed.IPAddresses) != 2 {
				t.Fatal("explicit SANs lost", parsed)
			}
			info, e := os.Stat(keyOut)
			if e != nil || info.Mode().Perm() != 0600 {
				t.Fatal("private key permissions unsafe", e)
			}
		})
	}
}

func TestControllerKeyRotationPreservesAllSecretOwnership(t *testing.T) {
	for _, stage := range []string{"success", "invalid key", "certificate", "account", "compose", "environment", "resolved", "operation", "control"} {
		t.Run(stage, func(t *testing.T) {
			cfg, path := commandConfigFixture(t)
			writeCommandConfig(t, cfg, path)
			if _, e := commandOutput(t, "migrate", "--config", path); e != nil {
				t.Fatal(e)
			}
			store, e := storage.Open(cfg.Storage.Driver, cfg.Storage.DSN)
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			ctx := context.Background()
			oldRing, e := security.NewKeyring("old", cfg.Security.EncryptionKeys)
			if e != nil {
				t.Fatal(e)
			}
			c := core.New(store, cfg, oldRing)
			pool, e := c.CreatePool(ctx, core.CreatePoolInput{Name: "isolated", BackendNetwork: "nsc-isolated", ChallengeUpstream: "http://127.0.0.1:1"})
			if e != nil {
				t.Fatal(e)
			}
			now := time.Now()
			rt := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "isolated", Type: model.RuntimeDocker, Endpoint: "unix:///isolated-nsc-fixture.sock", Enabled: true, ManagementEnabled: true, Status: model.RuntimeOnline, Metadata: model.Metadata{Revision: 1, CreatedAt: now, UpdatedAt: now}}
			if e = store.CreateRuntime(ctx, &rt); e != nil {
				t.Fatal(e)
			}
			encrypt := func(plaintext, aad string) []byte {
				b, e := oldRing.Encrypt([]byte(plaintext), []byte(aad))
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			cert := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "fixture", Status: model.CertificateActive, EncryptedPrivateKey: nil, EncryptionKeyID: "old", CreatedAt: now, UpdatedAt: now}
			cert.EncryptedPrivateKey = encrypt("certificate secret", cert.ID)
			if stage == "certificate" {
				cert.EncryptedPrivateKey = []byte("corrupt")
			}
			if e = store.CreateCertificate(ctx, &cert); e != nil {
				t.Fatal(e)
			}
			account := model.ACMEAccount{ID: "account", DirectoryURL: "https://acme.invalid/directory", Email: "test@example.test", Registration: "{}", EncryptedPrivateKey: encrypt("account secret", "account"), EncryptionKeyID: "old", CreatedAt: now, UpdatedAt: now}
			if stage == "account" {
				account.EncryptedPrivateKey = []byte("corrupt")
			}
			if e = store.UpsertACMEAccount(ctx, &account); e != nil {
				t.Fatal(e)
			}
			app, op, _, e := c.InstallApplication(ctx, core.InstallApplicationInput{ApplicationPreflightInput: core.ApplicationPreflightInput{RuntimeID: rt.ID, ComposeYAML: "services:\n  web:\n    image: nginx:alpine\n"}, Name: "secret-app", ProjectName: "secret-app", IdempotencyKey: "fixture"})
			if e != nil {
				t.Fatal(e)
			}
			operationCipher := encrypt("operation secret", "deployment-operation:"+op.ID)
			if stage == "operation" {
				operationCipher = []byte("corrupt")
			}
			if _, e = store.DB().ExecContext(ctx, "UPDATE deployment_operations SET encrypted_input=?,encryption_key_id='old' WHERE id=?", operationCipher, op.ID); e != nil {
				t.Fatal(e)
			}
			if stage == "compose" || stage == "environment" || stage == "resolved" {
				if _, e = store.DB().ExecContext(ctx, "UPDATE application_revisions SET encrypted_"+stage+"=? WHERE id=?", []byte("corrupt"), op.RevisionID); e != nil {
					t.Fatal(e)
				}
			}
			cipher := encrypt(`{"value":"control secret"}`, "control:variable-fixture")
			if stage == "control" {
				cipher = []byte("corrupt")
			}
			payload, e := json.Marshal(map[string]any{"ciphertext": cipher, "key_id": "old"})
			if e != nil {
				t.Fatal(e)
			}
			if e = store.WriteControlDocuments(ctx, []storage.ControlWrite{{Document: storage.ControlDocument{ID: "variable-project", Kind: "project", Payload: json.RawMessage(`{}`)}}, {Document: storage.ControlDocument{ID: "variable-owner", Kind: "environment", ParentID: "variable-project", Payload: json.RawMessage(`{}`)}}, {Document: storage.ControlDocument{ID: "variable-fixture", Kind: "variables", ParentID: "variable-owner", Payload: payload}}}); e != nil {
				t.Fatal(e)
			}
			if stage == "invalid key" {
				cfg.Security.EncryptionKeys["new"] = "invalid"
				writeCommandConfig(t, cfg, path)
			}
			output, e := commandOutput(t, "keys", "reencrypt", "--config", path)
			if stage != "success" {
				if e == nil {
					t.Fatal("corrupt encryption material accepted", stage)
				}
				return
			}
			if e != nil || !strings.Contains(output, "re-encrypted 1 certificate private keys, 1 ACME account keys, 1 application revisions, and 1 deployment operation inputs") {
				t.Fatal("rotation counts inaccurate", output, e)
			}
			newRing, e := security.NewKeyring("new", map[string]string{"new": cfg.Security.EncryptionKeys["new"]})
			if e != nil {
				t.Fatal(e)
			}
			controlAfter, e := store.GetControlDocument(ctx, "variable-fixture")
			if e != nil {
				t.Fatal(e)
			}
			var controlEnvelope struct {
				Ciphertext []byte `json:"ciphertext"`
				KeyID      string `json:"key_id"`
			}
			if e = json.Unmarshal(controlAfter.Payload, &controlEnvelope); e != nil {
				t.Fatal(e)
			}
			controlPlain, _, e := newRing.Decrypt(controlEnvelope.Ciphertext, []byte("control:variable-fixture"))
			if e != nil || string(controlPlain) != `{"value":"control secret"}` || controlEnvelope.KeyID != "new" || controlAfter.Revision != 1 || controlAfter.ParentID != "variable-owner" {
				t.Fatal("control rotation altered content, ownership or business revision", e)
			}
			certAfter, e := store.GetCertificate(ctx, cert.ID)
			if e != nil {
				t.Fatal(e)
			}
			accountAfter, e := store.GetACMEAccount(ctx, account.DirectoryURL, account.Email)
			if e != nil {
				t.Fatal(e)
			}
			revision, e := store.GetApplicationRevision(ctx, op.RevisionID)
			if e != nil {
				t.Fatal(e)
			}
			opAfter, e := store.GetDeploymentOperation(ctx, op.ID)
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range []struct {
				cipher    []byte
				aad, want string
			}{{certAfter.EncryptedPrivateKey, cert.ID, "certificate secret"}, {accountAfter.EncryptedPrivateKey, account.ID, "account secret"}, {opAfter.EncryptedInput, "deployment-operation:" + op.ID, "operation secret"}} {
				plain, key, e := newRing.Decrypt(v.cipher, []byte(v.aad))
				if e != nil || key != "new" || string(plain) != v.want {
					t.Fatal("rotated secret ownership/content lost", e)
				}
			}
			if revision.EncryptionKeyID != "new" || revision.ApplicationID != app.ID || opAfter.ApplicationID != app.ID || opAfter.Status != op.Status {
				t.Fatal("rotation altered application lifecycle")
			}
			for _, v := range []struct {
				part   string
				cipher []byte
			}{{"compose", revision.EncryptedCompose}, {"environment", revision.EncryptedEnvironment}, {"resolved", revision.EncryptedResolved}} {
				if _, _, e = newRing.Decrypt(v.cipher, []byte("application-revision:"+revision.ID+":"+v.part)); e != nil {
					t.Fatal("revision secrets were not rotated", v.part, e)
				}
			}
		})
	}
}
