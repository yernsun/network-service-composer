package agent

import nginxx "github.com/yernsun/network-service-composer/internal/nginx"

func (s GenerationState) needsRendererUpgrade() bool {
	return s.ConfigRevision > 0 && s.RendererVersion != "" && s.RendererVersion != nginxx.RendererVersion
}

// A renderer upgrade may regenerate the identical, authenticated model at the
// same revision. Other replays, changed models, and lower revisions stay denied.
func (s GenerationState) acceptsSnapshotRevision(revision int64, digest string) bool {
	return revision > s.ConfigRevision || revision == s.ConfigRevision && s.needsRendererUpgrade() && validDigest(digest) && digest == s.ModelDigest
}
