package nodecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/moby/moby/api/types/volume"
)

// VolumeInfo is safe display metadata, separate from retention's local-volume maps.
// Driver options and arbitrary labels may contain credentials and are never exposed.
type VolumeInfo struct {
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
}

func VolumeMetadata(v volume.Volume) VolumeInfo {
	kind := "unknown"
	if _, ok := v.Labels["com.docker.volume.anonymous"]; ok || v.Labels["com.nsc.volume.kind"] == "anonymous" {
		kind = "anonymous"
	} else if v.Labels["com.nsc.volume.kind"] == "named" || v.Labels["com.docker.compose.volume"] != "" {
		kind = "named"
	}
	// The fingerprint includes driver options without returning them to the browser.
	raw, _ := json.Marshal(struct {
		Name, Driver, Scope, CreatedAt string
		Labels, Options                map[string]string
	}{v.Name, v.Driver, v.Scope, v.CreatedAt, v.Labels, v.Options})
	digest := sha256.Sum256(raw)
	return VolumeInfo{Name: v.Name, Driver: v.Driver, Scope: v.Scope, CreatedAt: v.CreatedAt, Kind: kind, Version: hex.EncodeToString(digest[:])}
}
