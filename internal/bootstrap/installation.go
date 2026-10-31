package bootstrap

import "time"

const InstallationDocumentID = "system:installation"
const InstallationDocumentKind = "system-installation"

// Installation is the non-secret, database/Swarm projection of the private
// journal. Local recovery paths, SSH keys, DSNs and credentials are excluded.
type Installation struct {
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
