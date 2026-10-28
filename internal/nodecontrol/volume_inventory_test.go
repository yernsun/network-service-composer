package nodecontrol

import (
	"encoding/json"
	"github.com/moby/moby/api/types/volume"
	"strings"
	"testing"
)

func TestVolumeMetadataUsesEvidenceAndHidesDriverCredentials(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels map[string]string
		kind   string
	}{
		{"opaque", nil, "unknown"},
		{strings.Repeat("a", 64), nil, "unknown"},
		{"anonymous", map[string]string{"com.docker.volume.anonymous": ""}, "anonymous"},
		{"created-anonymous", map[string]string{"com.nsc.volume.kind": "anonymous"}, "anonymous"},
		{"created-named", map[string]string{"com.nsc.volume.kind": "named"}, "named"},
		{"compose", map[string]string{"com.docker.compose.volume": "data"}, "named"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := volume.Volume{Name: tc.name, Driver: "local", Scope: "local", Labels: tc.labels, CreatedAt: "2026-10-01T00:00:00Z", Options: map[string]string{"password": "private-credential"}}
			metadata := VolumeMetadata(v)
			raw, _ := json.Marshal(metadata)
			if metadata.Kind != tc.kind || strings.Contains(string(raw), "private-credential") {
				t.Fatalf("invalid display metadata: %s", raw)
			}
			v.Options["password"] = "new-credential"
			if metadata.Version == VolumeMetadata(v).Version {
				t.Fatal("changed driver options did not invalidate fingerprint")
			}
		})
	}
}
