package bootstrap

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

func signer(t *testing.T) (ssh.Signer, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(key, "isolated bootstrap test")
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return signer, pem.EncodeToMemory(block)
}

// sshFixture authenticates real SSH and serves fixed commands. It does not
// execute a shell or address Docker. Real SSH/Docker acceptance is separate.
func sshFixture(t *testing.T) (Host, ssh.Signer) {
	t.Helper()
	hostKey, _ := signer(t)
	userKey, private := signer(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if string(key.Marshal()) != string(userKey.PublicKey().Marshal()) {
			return nil, fmt.Errorf("wrong client identity")
		}
		return nil, nil
	}}
	server.AddHostKey(hostKey)
	var wg sync.WaitGroup
	var mu sync.Mutex
	clients := []net.Conn{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			clients = append(clients, connection)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer connection.Close()
				client, channels, requests, err := ssh.NewServerConn(connection, server)
				if err != nil {
					return
				}
				defer client.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						defer channel.Close()
						for request := range requests {
							if request.Type != "exec" {
								request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							ssh.Unmarshal(request.Payload, &payload)
							request.Reply(true, nil)
							switch payload.Command {
							case "echo-stream":
								io.Copy(channel, channel)
							case "wait":
								io.Copy(io.Discard, channel)
							case "fixed-output":
								io.WriteString(channel, "verified-node")
							case "fail-private":
								io.WriteString(channel.Stderr(), "do-not-print-this-secret")
								channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{17}))
								return
							case LocalDockerCLI + " system dial-stdio":
								request, err := http.ReadRequest(bufio.NewReader(channel))
								if err != nil {
									return
								}
								body, err := io.ReadAll(request.Body)
								if err != nil {
									return
								}
								request.Body.Close()
								status, data := 200, `{"ID":"actual-engine-id"}`
								if request.URL.Path == "/v1.47/create" {
									var input map[string]string
									if json.Unmarshal(body, &input) != nil || input["name"] != "safe" {
										status = 400
									} else {
										data = `{"Id":"receipt-from-engine"}`
									}
								}
								if request.URL.Path == "/v1.47/timeout-response" {
									status = 408
									data = `{"message":"do-not-print-this-secret"}`
								}
								if request.URL.Path == "/v1.47/temporary" {
									status = 503
									data = `{"message":"do-not-print-this-secret"}`
								}
								if request.URL.Path == "/v1.47/private-create" {
									if request.Header.Get("X-Registry-Auth") != "isolated-authorization" || string(body) != `{"name":"safe"}` {
										status = 403
									}
								}
								if request.URL.Path == "/v1.47/rejected" {
									status = 409
									data = `{"message":"do-not-print-this-secret"}`
								}
								if request.URL.Path == "/v1.47/malformed" {
									data = "malformed"
								}
								response := http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(data)), ContentLength: int64(len(data))}
								response.Write(channel)
							default:
								channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{1}))
								return
							}
							channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for _, c := range clients {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	os.WriteFile(keyPath, private, 0o600)
	known := filepath.Join(dir, "known_hosts")
	os.WriteFile(known, []byte(knownhosts.Line([]string{listener.Addr().String()}, hostKey.PublicKey())+"\n"), 0o600)
	port, _ := strconv.Atoi(strings.Split(listener.Addr().String(), ":")[1])
	return Host{Node: Node{Name: "isolated", Address: "127.0.0.1", SSHPort: port, SSHUser: "root", SSHKeyFile: keyPath}, KnownHostsFile: known}, userKey
}

func TestSSHIdentityHTTPTransportAndPrivateErrors(t *testing.T) {
	h, _ := sshFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	data, err := h.Run(ctx, "fixed-output", nil)
	if err != nil || string(data) != "verified-node" {
		t.Fatal("SSH execution", err)
	}
	if _, err = h.Run(ctx, "fail-private", nil); err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("private diagnostics escaped", err)
	}
	var output struct{ ID string }
	if err = h.Engine(ctx, "GET", "/info", nil, &output); err != nil || output.ID != "actual-engine-id" {
		t.Fatal("Engine stdio read", err)
	}
	if err = h.Engine(ctx, "POST", "/create", map[string]string{"name": "safe"}, &output); err != nil || output.ID != "receipt-from-engine" {
		t.Fatal("Engine stdio creation receipt", err)
	}
	if err = h.Engine(ctx, "GET", "/info", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = h.Engine(ctx, "POST", "/rejected", nil, nil); err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("Docker error exposed secrets", err)
	}
	h.DiagnosticsDir = filepath.Join(t.TempDir(), "diagnostics")
	if err = h.Engine(ctx, "POST", "/rejected", nil, nil); err == nil {
		t.Fatal("rejection hidden")
	}
	files, err := os.ReadDir(h.DiagnosticsDir)
	if err != nil || len(files) != 1 {
		t.Fatal("private diagnostic missing", err)
	}
	info, err := files[0].Info()
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("diagnostic is public")
	}
	if data := mustRead(t, filepath.Join(h.DiagnosticsDir, files[0].Name())); !strings.Contains(string(data), "do-not-print-this-secret") {
		t.Fatal("raw private evidence was not retained")
	}
	if err = h.Engine(ctx, "GET", "/malformed", nil, &output); err == nil {
		t.Fatal("invalid Engine evidence accepted")
	}
	if err = h.Engine(ctx, "POST", "/create", make(chan int), nil); err == nil {
		t.Fatal("non-JSON input accepted")
	}
	if err = h.Engine(ctx, "bad method", "/info", nil, nil); err == nil {
		t.Fatal("invalid request method accepted")
	}
	wrong, _ := signer(t)
	os.WriteFile(h.KnownHostsFile, []byte(knownhosts.Line([]string{net.JoinHostPort(h.Node.Address, strconv.Itoa(h.Node.SSHPort))}, wrong.PublicKey())), 0o600)
	if err = h.Engine(ctx, "POST", "/create", nil, nil); err == nil {
		t.Fatal("changed SSH host identity permitted an external write")
	}
	if h.Privileged("fixed") != "fixed" {
		t.Fatal("root wrapper")
	}
	h.Node.SSHUser = "operator"
	if h.Privileged("echo 'literal'") != "sudo -n -- sh -c 'echo '\"'\"'literal'\"'\"''" {
		t.Fatal("unsafe shell quoting", h.Privileged("echo 'literal'"))
	}
}

func TestTrustedHostAlgorithmSelectionIncludesHashedHosts(t *testing.T) {
	address := "192.0.2.29:2222"
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.29"), Port: 2222}
	keys := []ssh.Signer{}
	_, ed, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edSigner, _ := ssh.NewSignerFromKey(ed)
	keys = append(keys, edSigner)
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, signer)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaSigner, _ := ssh.NewSignerFromKey(rsaKey)
	keys = append(keys, rsaSigner)
	file := filepath.Join(t.TempDir(), "known_hosts")
	for _, key := range keys {
		line := knownhosts.HashHostname(knownhosts.Normalize(address)) + " " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))
		os.WriteFile(file, []byte(line+"\n"), 0o600)
		callback, err := knownhosts.New(file)
		if err != nil {
			t.Fatal(err)
		}
		algorithms, err := trustedHostAlgorithms(callback, address, remote)
		if err != nil || len(algorithms) < 2 {
			t.Fatal("trusted algorithm missing", err)
		}
		for _, algorithm := range algorithms {
			if algorithm == ssh.KeyAlgoRSA {
				t.Fatal("legacy RSA/SHA-1 enabled")
			}
		}
	}
	callback := func(string, net.Addr, ssh.PublicKey) error { return &knownhosts.KeyError{} }
	if _, err = trustedHostAlgorithms(callback, address, remote); err == nil {
		t.Fatal("unknown host accepted")
	}
	callback = func(string, net.Addr, ssh.PublicKey) error {
		return &knownhosts.KeyError{Want: []knownhosts.KnownKey{{Key: unsupportedKey{}}}}
	}
	if _, err = trustedHostAlgorithms(callback, address, remote); err == nil {
		t.Fatal("unsupported key accepted")
	}
}

type unsupportedKey struct{}

func (unsupportedKey) Type() string                        { return "unsupported" }
func (unsupportedKey) Marshal() []byte                     { return nil }
func (unsupportedKey) Verify([]byte, *ssh.Signature) error { return errors.New("unsupported") }

func TestSSHAgentAndStreamDeadlines(t *testing.T) {
	h, userKey := sshFixture(t)
	keyring := agent.NewKeyring()
	private, err := ssh.ParseRawPrivateKey(mustRead(t, h.Node.SSHKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if err = keyring.Add(agent.AddedKey{PrivateKey: private}); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); agent.ServeAgent(keyring, conn) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", socket)
	h.Node.SSHKeyFile = ""
	if userKey == nil {
		t.Fatal("fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err = h.Run(ctx, "fixed-output", nil); err != nil {
		t.Fatal("agent authentication", err)
	}
	conn, err := h.Stream(ctx, "echo-stream")
	if err != nil {
		t.Fatal(err)
	}
	if conn.LocalAddr() == nil || conn.RemoteAddr() == nil {
		t.Fatal("stream identity missing")
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})
	if _, err = conn.Write([]byte("probe")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err = io.ReadFull(conn, data); err != nil || string(data) != "probe" {
		t.Fatal("stream data", err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	if _, err = conn.Read(data); err == nil {
		t.Fatal("read deadline did not terminate blocked transport")
	}
	conn.Close()
	conn.Close()
	if err = conn.SetDeadline(time.Time{}); err == nil {
		t.Fatal("closed stream accepted deadline")
	}
	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "missing"))
	if _, err = h.Run(ctx, "fixed-output", nil); err == nil {
		t.Fatal("missing agent silently bypassed authentication")
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	if _, err = h.Run(ctx, "fixed-output", nil); err == nil {
		t.Fatal("missing credentials accepted")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestPrivateReferencesAndReadOnlyStatus(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "credential")
	os.WriteFile(file, []byte("ephemeral-test-value"), 0o600)
	if data, err := ReadPrivate(file); err != nil || string(data) != "ephemeral-test-value" {
		t.Fatal(err)
	}
	os.Chmod(file, 0o644)
	if _, err := ReadPrivate(file); err == nil {
		t.Fatal("public credential file accepted")
	}
	os.Chmod(file, 0o600)
	link := filepath.Join(dir, "link")
	os.Symlink(file, link)
	if _, err := ReadPrivate(link); err == nil {
		t.Fatal("credential symlink accepted")
	}
	if _, err := ReadPrivate(dir); err == nil {
		t.Fatal("directory credential accepted")
	}
	if _, err := ReadPrivate(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing credential accepted")
	}
	os.WriteFile(file, make([]byte, (1<<20)+1), 0o600)
	if _, err := ReadPrivate(file); err == nil {
		t.Fatal("oversized credential accepted")
	}
	j, r, _, _, _ := setup(t)
	actual, err := ReadJournal(j.dir)
	if err != nil || actual.ID != r.ID {
		t.Fatal("status could not observe a live writer", err)
	}
	if _, err := ReadJournal(dir); err == nil {
		t.Fatal("missing checkpoint accepted")
	}
	os.Chmod(dir, 0o755)
	if _, err := ReadJournal(dir); err == nil {
		t.Fatal("public status directory accepted")
	}
}

func TestSSHCommandStreamsRetainExitFailuresAndHalfClose(t *testing.T) {
	host, _ := sshFixture(t)
	stream, err := host.Stream(t.Context(), "fail-private")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(stream)
	stream.Close()
	if err == nil || strings.Contains(err.Error(), "do-not-print-this-secret") {
		t.Fatal("stream command exit was lost or leaked", err)
	}
	stream, err = host.Stream(t.Context(), "echo-stream")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(stream); done <- data }()
	if _, err = stream.Write([]byte("archive-data")); err != nil {
		t.Fatal(err)
	}
	if err = stream.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-done:
		if string(data) != "archive-data" {
			t.Fatal("stream changed")
		}
	case <-time.After(time.Second):
		t.Fatal("half-close did not terminate the remote command")
	}
	stream.Close()
}

func TestSSHUnavailableCredentialsAndUncertainDockerErrorsRemainBounded(t *testing.T) {
	h, _ := sshFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := h.Engine(ctx, "POST", "/private-create", RegistryRequest{Body: map[string]string{"name": "safe"}, Authorization: "isolated-authorization"}, nil); err != nil {
		t.Fatal("registry header was lost", err)
	}
	err := h.Engine(ctx, "POST", "/temporary", nil, nil)
	var rejected *Rejected
	if err == nil || errors.As(err, &rejected) || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("uncertain server failure lost its boundary", err)
	}
	h.DiagnosticsDir = filepath.Join(t.TempDir(), "not-a-directory")
	os.WriteFile(h.DiagnosticsDir, []byte("private"), 0600)
	if err = h.Engine(ctx, "POST", "/rejected", nil, nil); err == nil {
		t.Fatal("diagnostics failure hid the original rejection")
	}
	original := h.Node.SSHKeyFile
	h.Node.SSHKeyFile = filepath.Join(t.TempDir(), "missing")
	if _, err = h.Run(ctx, "fixed-output", nil); err == nil {
		t.Fatal("missing SSH key ignored")
	}
	h.Node.SSHKeyFile = original
	os.WriteFile(original, []byte("invalid-private-key"), 0600)
	if _, err = h.Stream(ctx, "fixed-output"); err == nil {
		t.Fatal("invalid SSH key ignored")
	}
	h.KnownHostsFile = filepath.Join(t.TempDir(), "missing")
	if _, err = h.Run(ctx, "fixed-output", nil); err == nil {
		t.Fatal("missing trusted-host file ignored")
	}
	stopped := false
	output := boundedOutput{limit: 5, stop: func() { stopped = true }}
	if n, err := output.Write([]byte("small")); err != nil || n != 5 {
		t.Fatal("bounded output rejected a valid response")
	}
	if _, err := output.Write([]byte("overflow")); err == nil || !stopped || output.Len() != 5 {
		t.Fatal("remote output was not stopped at its bound")
	}
}

func TestLockedAgentUnreachableTrustedHostAndTimeoutResponsesCannotAuthorizeChanges(t *testing.T) {
	host, _ := sshFixture(t)
	keyring := agent.NewKeyring()
	if err := keyring.Lock([]byte("isolated-lock-passphrase")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("", "nsc-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socket := filepath.Join(directory, "agent.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); agent.ServeAgent(keyring, conn) }()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", socket)
	original := host.Node.SSHKeyFile
	host.Node.SSHKeyFile = ""
	if _, err = host.Run(t.Context(), "fixed-output", nil); err == nil {
		t.Fatal("locked agent bypassed authentication")
	}
	t.Setenv("SSH_AUTH_SOCK", "")
	host.Node.SSHKeyFile = original
	err = host.Engine(t.Context(), "POST", "/timeout-response", nil, nil)
	var rejected *Rejected
	if err == nil || errors.As(err, &rejected) || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("timeout HTTP result was not retained as uncertain", err)
	}
	unreachable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := unreachable.Addr().String()
	unreachable.Close()
	_, port, _ := net.SplitHostPort(address)
	host.Node.SSHPort, _ = strconv.Atoi(port)
	trusted, _ := signer(t)
	os.WriteFile(host.KnownHostsFile, []byte(knownhosts.Line([]string{address}, trusted.PublicKey())), 0600)
	if _, err = host.Run(t.Context(), "fixed-output", nil); err == nil {
		t.Fatal("unreachable trusted host appeared available")
	}
}
