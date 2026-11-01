package bootstrap

import "time"

const InstallationDocumentID = "system:installation"
const InstallationDocumentKind = "system-installation"

// Installation is the non-secret, database/Swarm projection of the private
// journal. Local recovery paths, SSH keys, DSNs and credentials are excluded.
type Installation struct {
	ConsoleCertificateExpiresAt *time.Time       `json:"console_certificate_expires_at,omitempty"`
	ConsoleFingerprint          string           `json:"console_fingerprint,omitempty"`
	Executor                    Resource         `json:"executor"`
	Control                     Resource         `json:"control"`
	CredentialSecret            Resource         `json:"credential_secret"`
	MaintenanceNodeID           string           `json:"maintenance_node_id"`
	MaintenanceUID              int              `json:"maintenance_uid"`
	MaintenanceID               string           `json:"maintenance_id"`
	TrustSHA256                 string           `json:"trust_anchor_sha256,omitempty"`
	MaintenanceExpiresAt        *time.Time       `json:"maintenance_expires_at,omitempty"`
	MetadataLayout              int              `json:"metadata_layout"`
	CheckpointRevision          int64            `json:"checkpoint_revision"`
	Release                     *Release         `json:"release,omitempty"`
	History                     []ReleaseReceipt `json:"history"`
	OperationID                 string           `json:"operation_id,omitempty"`

	MaintenanceAnchor string              `json:"maintenance_anchor"`
	Topology          ManagerTopology     `json:"topology"`
	Health            string              `json:"health"`
	ID                string              `json:"id"`
	Revision          int64               `json:"revision"`
	Mode              string              `json:"mode"`
	State             State               `json:"state"`
	ClusterID         string              `json:"cluster_id"`
	PoolID            string              `json:"pool_id"`
	RuntimeID         string              `json:"runtime_id"`
	CertificateID     string              `json:"certificate_id,omitempty"`
	Components        map[string]Resource `json:"components"`
	Nodes             map[string]Identity `json:"nodes"`
	Database          DatabasePin         `json:"database"`
	ConsoleURLs       []string            `json:"console_urls"`
	ConfigDigest      string              `json:"config_digest"`
	IngressNetwork    string              `json:"ingress_network"`
	UpdatedAt         time.Time           `json:"updated_at"`
	AllowedOperations []Action            `json:"allowed_operations"`
	Drift             []string            `json:"drift"`
}

type DatabasePin struct {
	NodeID               string `json:"node_id"`
	EngineID             string `json:"engine_id"`
	Volume               string `json:"volume"`
	ServiceID            string `json:"service_id"`
	Image                string `json:"image"`
	SinglePointOfFailure bool   `json:"single_point_of_failure"`
}

// ManagerTopology describes the actual Swarm voting membership, including
// Managers outside the Controller inventory. It does not describe database HA.
type ManagerTopology struct {
	Managers          int      `json:"managers"`
	Reachable         int      `json:"reachable"`
	Majority          int      `json:"majority"`
	TolerableFailures int      `json:"tolerable_failures"`
	QuorumAvailable   bool     `json:"quorum_available"`
	NodeIDs           []string `json:"node_ids"`
}

// ValidateInstallation checks explicit registration receipts. Names, labels and
// legacy application Managed flags cannot establish installation ownership.
func ValidateInstallation(value Installation) error {
	if value.ID == "" || value.Revision < 1 || value.RuntimeID == "" || value.PoolID == "" || value.ClusterID == "" || value.MaintenanceAnchor == "" || len(value.Components) != 4 || !value.Database.SinglePointOfFailure {
		return ErrInstallationUnmanaged
	}
	ids := map[string]bool{}
	for _, key := range []string{"controller", "nginx", "nginx-sandbox", "postgresql"} {
		receipt := value.Components[key]
		if receipt.Kind != "service" || receipt.ID == "" || ids[receipt.ID] || receipt.SpecDigest == "" {
			return ErrInstallationUnmanaged
		}
		ids[receipt.ID] = true
	}
	if value.Database.ServiceID != value.Components["postgresql"].ID || value.Database.NodeID == "" || value.Database.EngineID == "" || value.Database.Volume == "" {
		return ErrInstallationUnmanaged
	}
	return nil
}
