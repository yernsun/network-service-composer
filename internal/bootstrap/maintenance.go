package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Release is operator-approved compatibility evidence. The execution driver also
// verifies the pulled image contracts before updating any service. Unknown
// schema/protocol transitions are blocked; image rollback never restores data.
type Release struct {
	Version         string   `json:"version"`
	Images          Images   `json:"images"`
	Protocol        int      `json:"protocol"`
	Schema          int      `json:"schema"`
	Configuration   int      `json:"configuration"`
	PostgreSQLMajor int      `json:"postgresql_major"`
	Architectures   []string `json:"architectures"`
}

func (r Release) Validate() error {
	if r.Version == "" || len(r.Version) > 64 {
		return fmt.Errorf("release version is required")
	}
	for _, image := range []string{r.Images.Controller, r.Images.Gateway, r.Images.PostgreSQL} {
		if !immutableImage.MatchString(image) {
			return fmt.Errorf("release images require immutable digests")
		}
	}
	if r.Protocol != 2 || r.Schema != 18 || r.Configuration != 1 || r.PostgreSQLMajor != 18 {
		return &MaintenanceError{Code: "incompatible_release"}
	}
	seen := map[string]bool{}
	for _, architecture := range r.Architectures {
		if (architecture != "amd64" && architecture != "arm64") || seen[architecture] {
			return fmt.Errorf("invalid release architecture")
		}
		seen[architecture] = true
	}
	if !seen["amd64"] || !seen["arm64"] {
		return fmt.Errorf("release must declare amd64 and arm64 compatibility")
	}
	return nil
}
func DecodeRelease(data []byte) (Release, error) {
	var value Release
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return value, fmt.Errorf("exactly one release manifest is required")
	}
	return value, value.Validate()
}

type MaintenanceError struct{ Code string }

func (e *MaintenanceError) Error() string { return "system installation operation blocked: " + e.Code }

var ErrInstallationUnmanaged = errors.New("installation has not been adopted")

type PlanRequest struct {
	Action     Action   `json:"action"`
	Revision   int64    `json:"revision"`
	Components []string `json:"components,omitempty"`
	Release    *Release `json:"release,omitempty"`
}
type ExecuteRequest struct {
	OperationID    string `json:"operation_id,omitempty"`
	PlanHash       string `json:"plan_hash"`
	Revision       int64  `json:"revision"`
	IdempotencyKey string `json:"-"`
	Actor          string `json:"-"`
}

type MaintenanceJob struct {
	ExecutionMode  string     `json:"execution_mode"`
	Acknowledged   bool       `json:"acknowledged"`
	TaskID         string     `json:"task_id,omitempty"`
	ID             string     `json:"id"`
	InstallationID string     `json:"installation_id"`
	State          State      `json:"state"`
	Plan           Plan       `json:"plan"`
	Operation      *Operation `json:"operation,omitempty"`
	Actor          string     `json:"actor"`
	Reason         string     `json:"reason,omitempty"`
	Revision       int64      `json:"revision"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// Expected/actual infrastructure receipts are stored separately from the
	// public response. There are no credentials or arbitrary commands here.
	Runner           Resource `json:"runner,omitempty"`
	Checkpoint       Resource `json:"checkpoint,omitempty"`
	CredentialSecret Resource `json:"-"`
	Key              string   `json:"-"`
}

type InstallationProvider interface {
	View(context.Context) (Installation, error)
	Plan(context.Context, PlanRequest) (Plan, error)
	Start(context.Context, ExecuteRequest) (MaintenanceJob, error)
	Operation(context.Context, string) (MaintenanceJob, error)
}

type ReleaseReceipt struct {
	Revision            int64               `json:"revision"`
	Release             Release             `json:"release"`
	Components          map[string]Resource `json:"components"`
	ConfigurationDigest string              `json:"configuration_digest"`
	NonImageDigests     map[string]string   `json:"non_image_digests"`
	RecordedAt          time.Time           `json:"recorded_at"`
}

// MaintenanceCredential is serialized only into a protected offline file or a
// recorded Swarm Secret. It is never included in an installation or operation.
type MaintenanceCredential struct {
	RegistryAuth       map[string]string `json:"registry_auth,omitempty"`
	DSN                string            `json:"dsn"`
	Certificate        string            `json:"certificate"`
	PrivateKey         string            `json:"private_key"`
	CA                 string            `json:"ca"`
	ConsoleCertificate string            `json:"console_certificate"`
}
