package id

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Kind string

const (
	Pool                Kind = "pool"
	Runtime             Kind = "rt"
	Service             Kind = "svc"
	Endpoint            Kind = "ep"
	Domain              Kind = "dom"
	Route               Kind = "route"
	Certificate         Kind = "cert"
	Gateway             Kind = "gw"
	Challenge           Kind = "challenge"
	Account             Kind = "account"
	Snapshot            Kind = "snap"
	Audit               Kind = "audit"
	CatalogSource       Kind = "catsrc"
	CatalogVersion      Kind = "catver"
	Application         Kind = "app"
	ApplicationRevision Kind = "apprev"
	Operation           Kind = "op"
)

func New(kind Kind) string {
	return string(kind) + "_" + uuid.Must(uuid.NewV7()).String()
}

func Validate(value string, kind Kind) error {
	prefix := string(kind) + "_"
	if !strings.HasPrefix(value, prefix) {
		return fmt.Errorf("identifier %q must begin with %q", value, prefix)
	}
	parsed, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	if err != nil {
		return fmt.Errorf("invalid %s identifier: %w", kind, err)
	}
	if parsed.Version() != 7 {
		return fmt.Errorf("invalid %s identifier: UUID version must be 7", kind)
	}
	return nil
}
