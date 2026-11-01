package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"github.com/yernsun/network-service-composer/internal/bootstrap/install"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func maintenanceSSH(t *testing.T, machine string) (bootstrap.Node, string) {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "isolated-cli-fixture")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(dir, "known_hosts")
	os.WriteFile(known, []byte(knownhosts.Line([]string{listener.Addr().String()}, signer.PublicKey())+"\n"), 0600)
	config := &ssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := []net.Conn{}
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections = append(connections, conn)
			mu.Unlock()
			wg.Go(func() {
				defer conn.Close()
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for request := range channels {
					channel, requests, err := request.Accept()
					if err != nil {
						return
					}
					wg.Go(func() {
						defer channel.Close()
						for request := range requests {
							if request.Type != "exec" {
								request.Reply(false, nil)
								continue
							}
							var value struct{ Command string }
							ssh.Unmarshal(request.Payload, &value)
							request.Reply(true, nil)
							exit := uint32(1)
							if value.Command == "cat /etc/machine-id" {
								io.WriteString(channel, machine)
								exit = 0
							}
							channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exit}))
							return
						}
					})
				}
			})
		}
	})
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	return bootstrap.Node{Name: "one", Address: "127.0.0.1", SSHPort: number, SSHUser: "root", SSHKeyFile: keyFile, Manager: true}, known
}

func TestDefaultBootstrapTransportAnchorsTheMaintenanceWriter(t *testing.T) {
	node, known := maintenanceSSH(t, "isolated-maintenance-machine")
	path, dir := cliConfig(t)
	data, _ := bootstrap.ReadPrivate(path)
	cfg, err := bootstrap.DecodeConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Nodes = []bootstrap.Node{node}
	cfg.KnownHostsFile = known
	journal, err := bootstrap.OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	record, err := journal.Initialize(cfg, bootstrap.Digest("isolated-maintenance-machine"))
	if err != nil {
		t.Fatal(err)
	}
	session, err := connectBootstrap(t.Context(), cfg, &record, journal, dir, bootstrapSessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := session.(installationSession)
	driver, hasDriver := actual.driver.(*install.Installer)
	if !ok || !hasDriver || driver.Hosts[node.Name] == nil {
		t.Fatal("independent session has no verified transport")
	}
	if _, err = session.Plan(t.Context(), bootstrap.Init); err == nil {
		t.Fatal("unobserved Docker accepted")
	}
	if _, err = session.Execute(t.Context(), bootstrap.Plan{}, "", "bad"); err == nil {
		t.Fatal("unbound executor accepted")
	}
	if _, err = session.Resume(t.Context(), "missing-operation"); err == nil {
		t.Fatal("missing operation resumed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err = session.Verify(ctx, io.Discard); err == nil {
		t.Fatal("missing installed evidence verified")
	}
	record.MaintenanceIdentity = bootstrap.Digest("copied-checkpoint-from-another-machine")
	if _, err = connectBootstrap(t.Context(), cfg, &record, journal, dir, bootstrapSessionOptions{}); err == nil || !strings.Contains(err.Error(), "recorded maintenance node") {
		t.Fatal("copied journal authorized a different writer", err)
	}
	cfg.KnownHostsFile = filepath.Join(t.TempDir(), "missing")
	if _, err = connectBootstrap(context.Background(), cfg, &record, journal, dir, bootstrapSessionOptions{}); err == nil {
		t.Fatal("untrusted SSH target accepted")
	}
}
