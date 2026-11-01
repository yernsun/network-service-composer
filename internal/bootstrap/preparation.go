package bootstrap

// PreparationPlan contains public approval evidence only. Original CA/private
// configuration references remain solely in the node-local journal.
type PreparationPlan struct {
	Proof       string `json:"proof"`
	TrustSHA256 string `json:"trust_anchor_sha256"`
}

type PreparationReferences struct {
	CACertificateFile         string            `json:"ca_certificate_file"`
	CAPrivateKeyFile          string            `json:"ca_private_key_file"`
	ConfigurationFile         string            `json:"configuration_file,omitempty"`
	PreviousCertificateID     string            `json:"previous_certificate_id,omitempty"`
	SourceConfigurationHashes map[string]string `json:"source_configuration_hashes"`
}
