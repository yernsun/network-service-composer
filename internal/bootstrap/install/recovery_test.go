package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type archiveConn struct {
	io.Reader
	io.Writer
	closeErr error
}

func (*archiveConn) Close() error                     { return nil }
func (c *archiveConn) CloseWrite() error              { return c.closeErr }
func (*archiveConn) LocalAddr() net.Addr              { return &net.UnixAddr{} }
func (*archiveConn) RemoteAddr() net.Addr             { return &net.UnixAddr{} }
func (*archiveConn) SetDeadline(time.Time) error      { return nil }
func (*archiveConn) SetReadDeadline(time.Time) error  { return nil }
func (*archiveConn) SetWriteDeadline(time.Time) error { return nil }

func TestDatabaseRecoveryArchiveIsPrivateVerifiedAndNeverMovesData(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	host := i.Hosts["one"].(*fakeHost)
	dump := []byte("PGDMPisolated-fixture-archive")
	mode := ""
	var received bytes.Buffer
	host.stream = func(_ context.Context, command string) (net.Conn, error) {
		if strings.Contains(command, "pg_dump") {
			if mode == "unavailable" {
				return nil, io.EOF
			}
			data := dump
			if mode == "truncated" {
				data = []byte("PG")
			}
			return &archiveConn{Reader: bytes.NewReader(data)}, nil
		}
		if !strings.Contains(command, "pg_restore --list") {
			t.Fatal("unapproved backup command")
		}
		if mode == "verification" {
			return nil, io.EOF
		}
		return &archiveConn{Reader: bytes.NewReader(nil), Writer: &received}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	r, err := i.backupDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received.Bytes(), dump) {
		t.Fatal("archive validation did not consume the backup")
	}
	if err = i.Check(ctx, bootstrap.Step{Key: "backup", Receipt: &r}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(i.Directory, "backups", r.ID))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("archive is not private", err)
	}
	for _, failure := range []string{"unavailable", "truncated", "verification"} {
		mode = failure
		if _, err = i.backupDatabase(ctx); err == nil {
			t.Fatal("invalid recovery archive accepted", failure)
		}
	}
	files, _ := os.ReadDir(filepath.Join(i.Directory, "backups"))
	if len(files) != 1 {
		t.Fatal("incomplete backup retained as a usable archive")
	}
	if err = os.WriteFile(filepath.Join(i.Directory, "backups", r.ID), []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = i.checkBackup(r); err == nil {
		t.Fatal("modified backup accepted")
	}
	r.ID = "../database.dump"
	if err = i.checkBackup(r); err == nil {
		t.Fatal("external backup reference accepted")
	}
	if len(host.volumes) != 1 {
		t.Fatal("backup changed the database volume")
	}
}

func TestCompletedIdentityCreationReconcilesButPartialOrModifiedKeysRemainUnknown(t *testing.T) {
	i, _ := installerFixture(t, 1)
	if err := i.makeMaterials(); err != nil {
		t.Fatal(err)
	}
	receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "materials", State: bootstrap.Unknown})
	if err != nil || receipt == nil {
		t.Fatal("completed creation not reconciled", err)
	}
	if err = i.Check(t.Context(), bootstrap.Step{Key: "materials", Receipt: receipt}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(i.material("session-key"), []byte("changed externally"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "materials", State: bootstrap.Unknown}); err == nil {
		t.Fatal("changed identity claimed")
	}
	if err = i.Check(t.Context(), bootstrap.Step{Key: "materials", Receipt: receipt}); err == nil {
		t.Fatal("identity drift ignored")
	}
	os.Remove(i.material("complete.json"))
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "materials", State: bootstrap.Unknown}); err == nil {
		t.Fatal("partial identity creation claimed")
	}
	if err = i.makeMaterials(); err == nil {
		t.Fatal("private identities silently replaced")
	}
	os.Symlink("/nonexistent", i.material("unexpected-link"))
	if _, err = i.materialDigest(); err == nil {
		t.Fatal("symbolic identity reference accepted")
	}
}

func TestProvisioningCredentialsAndPreflightFailureAreNotApplied(t *testing.T) {
	for _, scenario := range []string{"short-password", "public-password", "missing-password", "disk-malformed", "port-malformed", "endpoint-malformed", "preflight-unavailable", "frozen-engine"} {
		t.Run(scenario, func(t *testing.T) {
			i, c := installerFixture(t, 1)
			host := i.Hosts["one"].(*fakeHost)
			switch scenario {
			case "short-password", "public-password", "missing-password":
				i.Config.AdminPasswordFile = filepath.Join(t.TempDir(), "password")
				if scenario != "missing-password" {
					os.WriteFile(i.Config.AdminPasswordFile, []byte("too-short"), 0600)
				}
				if scenario == "public-password" {
					os.Chmod(i.Config.AdminPasswordFile, 0644)
				}
				i.Record.ConfigDigest = bootstrap.Digest(i.Config)
			case "disk-malformed":
				host.preflight = "660 not-a-size"
			case "port-malformed":
				host.preflight = "660 10485760 0000:zzzz"
			case "endpoint-malformed":
				host.preflight = "660 10485760 unrecognized"
			case "preflight-unavailable":
				host.runErr = errors.New("sudo unavailable")
			case "frozen-engine":
				identity := i.Record.Nodes["one"]
				identity.EngineID = "old-engine"
				i.Record.Nodes["one"] = identity
			}
			if _, err := i.Plan(t.Context(), bootstrap.Init); err == nil {
				t.Fatal("unsafe preflight allowed")
			}
			for _, call := range c.calls {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("mutation occurred after failed preflight")
				}
			}
		})
	}
}

type streamReadFailure struct{}

func (streamReadFailure) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type streamWriteFailure struct{}

func (streamWriteFailure) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestBackupReadWriteAndRemoteValidationFailuresNeverProduceUsableReceipts(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	host := i.Hosts["one"].(*fakeHost)
	for _, scenario := range []string{"dump-read", "validation-write", "validation-close", "validation-read", "no-half-close", "success"} {
		t.Run(scenario, func(t *testing.T) {
			host.stream = func(_ context.Context, command string) (net.Conn, error) {
				if strings.Contains(command, "pg_dump") {
					var reader io.Reader = bytes.NewReader([]byte("PGDMPisolated-archive"))
					if scenario == "dump-read" {
						reader = io.MultiReader(bytes.NewReader([]byte("PGDMP")), streamReadFailure{})
					}
					return &archiveConn{Reader: reader}, nil
				}
				connection := &archiveConn{Reader: bytes.NewReader(nil), Writer: io.Discard}
				switch scenario {
				case "validation-write":
					connection.Writer = streamWriteFailure{}
				case "validation-close":
					connection.closeErr = io.ErrClosedPipe
				case "validation-read":
					connection.Reader = streamReadFailure{}
				case "no-half-close":
					return struct{ net.Conn }{connection}, nil
				}
				return connection, nil
			}
			result, err := i.backupDatabase(t.Context())
			if scenario == "success" {
				if err != nil || i.checkBackup(result) != nil {
					t.Fatal("valid archive failed", err)
				}
			} else if err == nil || result.ID != "" {
				t.Fatal("incomplete archive exposed as recovery evidence")
			}
		})
	}
	files, _ := os.ReadDir(filepath.Join(i.Directory, "backups"))
	if len(files) != 1 {
		t.Fatal("failed archive was retained as a completed backup")
	}
	r := bootstrap.Resource{Kind: "backup", ID: "missing.dump"}
	if err := i.checkBackup(r); err == nil {
		t.Fatal("missing backup accepted")
	}
	receipt, err := i.Reconcile(t.Context(), bootstrap.Step{Key: "backup", State: bootstrap.Unknown})
	var retry *bootstrap.RetrySafe
	if receipt != nil || !errors.As(err, &retry) {
		t.Fatal("read-only backup cannot safely repeat", err)
	}
	delete(i.Record.Resources, "service:postgresql")
	if _, err = i.backupDatabase(t.Context()); err == nil {
		t.Fatal("unknown database backed up")
	}
	if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "backup", State: bootstrap.Unknown}); err == nil {
		t.Fatal("unknown database backup retried")
	}
}

func TestBackupCannotReplaceAnExistingFilesystemObject(t *testing.T) {
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	path := filepath.Join(i.Directory, "backups")
	original := []byte("isolated-existing-object")
	os.WriteFile(path, original, 0600)
	if _, err := i.backupDatabase(t.Context()); err == nil {
		t.Fatal("backup replaced a pre-existing filesystem object")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("original filesystem object overwritten")
	}
}
