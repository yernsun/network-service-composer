package storage

import (
	"context"
	"errors"
)

var ErrSystemProtected = errors.New("system project resources are protected")

type systemBootstrapKey struct{}

// WithSystemBootstrap is reserved for the configured deployment reconciler. No
// API input can select this context; normal writes and deletion workers cannot bypass protection.
func WithSystemBootstrap(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemBootstrapKey{}, true)
}

type SystemService struct {
	EnvironmentID string `json:"environment_id"`
	ProjectID     string `json:"project_id"`
	ClusterID     string `json:"cluster_id"`
	System        bool   `json:"system"`
}
