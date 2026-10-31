package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

// Backups never replace or relocate the original data volume. A custom-format
// archive is retained alongside the matching offline identities and journal.
func (i *Installer) backupDatabase(ctx context.Context) (bootstrap.Resource, error) {
	container, err := i.databaseContainer(ctx)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	dir := filepath.Join(i.Directory, "backups")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return bootstrap.Resource{}, err
	}
	file, err := os.CreateTemp(dir, "database-*.dump")
	if err != nil {
		return bootstrap.Resource{}, err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(file.Name())
		}
	}()
	host := i.Hosts[i.Config.DataNode]
	stream, err := host.Stream(ctx, host.Privileged("docker exec -i "+bootstrap.ShellQuote(container)+" pg_dump --format=custom --no-password --username=nsc --dbname=nsc"))
	if err != nil {
		return bootstrap.Resource{}, err
	}
	defer stream.Close()
	if deadline, ok := ctx.Deadline(); ok {
		stream.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { stream.Close() })
	defer stop()
	hash := sha256.New()
	reader := io.TeeReader(io.LimitReader(stream, (1<<30)+1), io.MultiWriter(file, hash))
	magic := make([]byte, 5)
	if _, err = io.ReadFull(reader, magic); err != nil || string(magic) != "PGDMP" {
		return bootstrap.Resource{}, fmt.Errorf("database backup did not produce a PostgreSQL archive")
	}
	size, err := io.Copy(io.Discard, reader)
	if err != nil || size+5 > 1<<30 {
		return bootstrap.Resource{}, fmt.Errorf("database backup did not complete within the local archive limit")
	}
	// pg_restore --list checks the archive footer/TOC after SSH has confirmed exit.
	// Restoring business data remains a separate, explicit recovery operation.
	if err = file.Sync(); err != nil {
		return bootstrap.Resource{}, err
	}
	if err = file.Close(); err != nil {
		return bootstrap.Resource{}, err
	}
	archive, err := os.Open(file.Name())
	if err != nil {
		return bootstrap.Resource{}, err
	}
	defer archive.Close()
	validation, err := host.Stream(ctx, host.Privileged("docker exec -i "+bootstrap.ShellQuote(container)+" pg_restore --list >/dev/null"))
	if err != nil {
		return bootstrap.Resource{}, err
	}
	defer validation.Close()
	if deadline, ok := ctx.Deadline(); ok {
		validation.SetDeadline(deadline)
	}
	stopValidation := context.AfterFunc(ctx, func() { validation.Close() })
	defer stopValidation()
	half, ok := validation.(interface{ CloseWrite() error })
	if !ok {
		return bootstrap.Resource{}, fmt.Errorf("SSH archive verification requires half-close support")
	}
	if _, err = io.Copy(validation, archive); err != nil {
		return bootstrap.Resource{}, err
	}
	if err = half.CloseWrite(); err != nil {
		return bootstrap.Resource{}, err
	}
	if _, err = io.Copy(io.Discard, validation); err != nil {
		return bootstrap.Resource{}, fmt.Errorf("database backup archive verification failed")
	}
	directory, err := os.Open(dir)
	if err != nil {
		return bootstrap.Resource{}, err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return bootstrap.Resource{}, err
	}
	complete = true
	return bootstrap.Resource{Kind: "backup", ID: filepath.Base(file.Name()), SpecDigest: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (i *Installer) checkBackup(r bootstrap.Resource) error {
	if filepath.Base(r.ID) != r.ID || !strings.HasSuffix(r.ID, ".dump") {
		return fmt.Errorf("invalid backup reference")
	}
	file, err := os.Open(filepath.Join(i.Directory, "backups", r.ID))
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != r.SpecDigest {
		return fmt.Errorf("recovery backup differs from its receipt")
	}
	return nil
}
