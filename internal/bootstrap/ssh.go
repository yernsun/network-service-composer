package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
	"golang.org/x/sys/unix"
)

var ErrNodeUnavailable = errors.New("SSH node is unavailable")

type Host struct {
	Node           Node
	KnownHostsFile string
	DiagnosticsDir string
}

func (h Host) connect(ctx context.Context) (*ssh.Client, error) {
	callback, err := knownhosts.New(h.KnownHostsFile)
	if err != nil {
		return nil, fmt.Errorf("read trusted SSH host identities: %w", err)
	}
	var auth []ssh.AuthMethod
	if h.Node.SSHKeyFile != "" {
		data, err := ReadPrivate(h.Node.SSHKeyFile)
		if err != nil {
			return nil, err
		}
		defer clear(data)
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("SSH key requires a valid unencrypted key file or an existing SSH agent")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, fmt.Errorf("connect SSH agent: %w", err)
		}
		defer conn.Close()
		signers, err := agent.NewClient(conn).Signers()
		if err != nil {
			return nil, err
		}
		// Authentication finishes before this connection is closed.
		auth = append(auth, ssh.PublicKeys(signers...))
		return h.dial(ctx, callback, auth)
	}
	return h.dial(ctx, callback, auth)
}

func (h Host) dial(ctx context.Context, callback ssh.HostKeyCallback, auth []ssh.AuthMethod) (*ssh.Client, error) {
	if len(auth) == 0 {
		return nil, fmt.Errorf("SSH agent or key-file authentication is required")
	}
	address := net.JoinHostPort(h.Node.Address, strconv.Itoa(h.Node.SSHPort))
	algorithms, err := trustedHostAlgorithms(callback, address, &net.TCPAddr{IP: net.ParseIP(h.Node.Address), Port: h.Node.SSHPort})
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, ErrNodeUnavailable
	}
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	cc, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: h.Node.SSHUser, Auth: auth, HostKeyCallback: callback, HostKeyAlgorithms: algorithms, Timeout: 10 * time.Second})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SSH identity or authentication verification failed: %w", err)
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, channels, requests), nil
}

// Negotiation must prefer keys actually trusted for this endpoint. OpenSSH may
// advertise several host keys while known_hosts contains only one of them.
// The official callback also resolves hashed hosts, patterns and revoked keys.
func trustedHostAlgorithms(callback ssh.HostKeyCallback, address string, remote net.Addr) ([]string, error) {
	probe, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		return nil, err
	}
	err = callback(address, remote, probe)
	var mismatch *knownhosts.KeyError
	if !errors.As(err, &mismatch) || len(mismatch.Want) == 0 {
		return nil, fmt.Errorf("SSH endpoint has no supported pre-trusted host identity")
	}
	algorithms := []string{}
	seen := map[string]bool{}
	for _, known := range mismatch.Want {
		var names []string
		switch known.Key.Type() {
		case ssh.KeyAlgoED25519:
			names = []string{ssh.CertAlgoED25519v01, ssh.KeyAlgoED25519}
		case ssh.KeyAlgoECDSA256:
			names = []string{ssh.CertAlgoECDSA256v01, ssh.KeyAlgoECDSA256}
		case ssh.KeyAlgoECDSA384:
			names = []string{ssh.CertAlgoECDSA384v01, ssh.KeyAlgoECDSA384}
		case ssh.KeyAlgoECDSA521:
			names = []string{ssh.CertAlgoECDSA521v01, ssh.KeyAlgoECDSA521}
		case ssh.KeyAlgoRSA:
			names = []string{ssh.CertAlgoRSASHA512v01, ssh.CertAlgoRSASHA256v01, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}
		}
		for _, name := range names {
			if !seen[name] {
				algorithms = append(algorithms, name)
				seen[name] = true
			}
		}
	}
	if len(algorithms) == 0 {
		return nil, fmt.Errorf("SSH endpoint requires a supported modern host key")
	}
	return algorithms, nil
}

// Run accepts executor-authored commands only. Never pass user-provided shell
// fragments; quote every path/address and transport secrets through stdin.
func (h Host) Run(ctx context.Context, command string, input []byte) ([]byte, error) {
	client, err := h.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(input)
	output := boundedOutput{limit: 32 << 20, stop: func() { client.Close() }}
	session.Stdout = &output
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	if err = session.Run(command); err != nil {
		return nil, fmt.Errorf("remote bootstrap command failed; inspect private node diagnostics")
	}
	return output.Bytes(), nil
}

type boundedOutput struct {
	bytes.Buffer
	limit int
	stop  func()
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		b.stop()
		return 0, fmt.Errorf("remote response exceeds limit")
	}
	return b.Buffer.Write(data)
}

func (h Host) Privileged(command string) string {
	if h.Node.SSHUser == "root" {
		return command
	}
	return "sudo -n -- sh -c " + ShellQuote(command)
}

func ShellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// Engine uses Docker's stdio HTTP transport inside the verified SSH channel.
// There is no TCP Docker listener and no credential in the remote command line.
// RegistryRequest carries only the Docker API registry authorization header.
// The value is never part of a service specification, journal or public result.
type RegistryRequest struct {
	Body          any
	Authorization string
}

func (h Host) Engine(ctx context.Context, method, path string, input, output any) error {
	var authorization string
	if private, ok := input.(RegistryRequest); ok {
		input, authorization = private.Body, private.Authorization
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.47"+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("X-Registry-Auth", authorization)
	}
	client, err := h.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	if err = session.Start(h.Privileged("docker system dial-stdio")); err != nil {
		return err
	}
	if err = request.Write(stdin); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(stdout), request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Docker diagnostics can include environment values or registry credentials.
		if h.DiagnosticsDir != "" {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			if readErr == nil && os.MkdirAll(h.DiagnosticsDir, 0o700) == nil {
				file, createErr := os.CreateTemp(h.DiagnosticsDir, "docker-response-*.json")
				if createErr == nil {
					_, writeErr := file.Write(data)
					if writeErr == nil {
						writeErr = file.Sync()
					}
					file.Close()
					if writeErr != nil {
						os.Remove(file.Name())
					}
				}
			}
		}
		reason := fmt.Sprintf("Docker API %s %s returned HTTP %d; inspect private executor diagnostics", method, path, response.StatusCode)
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != 408 {
			return &Rejected{Reason: reason}
		}
		return fmt.Errorf("%s", reason)
	}
	if output == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(output)
}

func ReadPrivate(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "private-reference")
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("credential must be a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err == nil && len(data) > 1<<20 {
		return nil, fmt.Errorf("credential file exceeds limit")
	}
	return data, err
}

// Stream is a private transport for executor-authored commands such as a TCP
// relay inside the pinned database container. It is not exposed through HTTP.
func (h Host) Stream(ctx context.Context, command string) (net.Conn, error) {
	client, err := h.connect(ctx)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		client.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		client.Close()
		return nil, err
	}
	if err = session.Start(command); err != nil {
		client.Close()
		return nil, err
	}
	return &commandConn{reader: stdout, writer: stdin, client: client, session: session, remote: &net.TCPAddr{IP: net.ParseIP(h.Node.Address), Port: h.Node.SSHPort}}, nil
}

type commandConn struct {
	reader                io.Reader
	session               *ssh.Session
	writer                io.Writer
	client                *ssh.Client
	remote                net.Addr
	once                  sync.Once
	mu                    sync.Mutex
	readTimer, writeTimer *time.Timer
	closed                bool
}

func (c *commandConn) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if err == io.EOF && c.session != nil {
		if exitErr := c.session.Wait(); exitErr != nil {
			return n, fmt.Errorf("remote stream failed")
		}
	}
	return n, err
}
func (c *commandConn) CloseWrite() error           { return c.writer.(io.Closer).Close() }
func (c *commandConn) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *commandConn) LocalAddr() net.Addr         { return &net.UnixAddr{Name: "bootstrap-ssh", Net: "unix"} }
func (c *commandConn) RemoteAddr() net.Addr        { return c.remote }
func (c *commandConn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		if c.readTimer != nil {
			c.readTimer.Stop()
		}
		if c.writeTimer != nil {
			c.writeTimer.Stop()
		}
		c.mu.Unlock()
		c.client.Close()
	})
	return nil
}
func (c *commandConn) deadline(timer **time.Timer, at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	if *timer != nil {
		(*timer).Stop()
	}
	if !at.IsZero() {
		*timer = time.AfterFunc(time.Until(at), func() { c.Close() })
	}
	return nil
}
func (c *commandConn) SetReadDeadline(at time.Time) error  { return c.deadline(&c.readTimer, at) }
func (c *commandConn) SetWriteDeadline(at time.Time) error { return c.deadline(&c.writeTimer, at) }
func (c *commandConn) SetDeadline(at time.Time) error {
	if err := c.SetReadDeadline(at); err != nil {
		return err
	}
	return c.SetWriteDeadline(at)
}
