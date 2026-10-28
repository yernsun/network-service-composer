package core

import (
	"fmt"
	"io/fs"

	"github.com/yernsun/network-service-composer/internal/marketplace"
)

// The embedded catalog is the production source. An FS boundary lets isolated
// tests exercise malformed distribution material without changing that source.
func builtinMarketSnapshotsFrom(files fs.FS) (map[string]string, string, error) {
	data, err := fs.ReadFile(files, "builtin/market.yaml")
	if err != nil {
		return nil, "", err
	}
	m, err := marketplace.Decode(data)
	if err != nil {
		return nil, "", err
	}
	if err = m.Validate(false); err != nil {
		return nil, "", err
	}
	sourceContents, err := builtinMarketDocuments(files, m.Marketplace.Docs)
	if err != nil {
		return nil, "", err
	}
	meta := m.Marketplace
	meta.Endpoint = "" // The built-in source does not claim the example publication endpoint.
	source := marketplace.JSON(marketplace.Snapshot{Metadata: meta, Documents: meta.Docs, Contents: sourceContents})
	result := map[string]string{}
	for _, a := range m.Apps {
		if len(a.Versions) == 0 {
			return nil, "", fmt.Errorf("builtin snapshots require a single baseline")
		}
		for vi, v := range a.Versions {
			if vi > 0 && v.Files.Swarm.Path == "" {
				return nil, "", fmt.Errorf("additional builtin versions require a Swarm template")
			}
			docs := make(map[string]marketplace.Document, len(v.Files.Manuals)+1)
			for key, doc := range v.Files.Manuals {
				docs[key] = doc
			}
			docs["readme"] = marketplace.Document{Title: marketplace.Text{"en": "README"}, Files: v.Files.Readme}
			text, e := builtinMarketDocuments(files, docs)
			if e != nil {
				return nil, "", e
			}
			appMeta := meta
			appMeta.Docs = nil
			key := a.ID
			if vi > 0 {
				key += "@" + v.Version
			}
			result[key] = marketplace.JSON(marketplace.Snapshot{Metadata: appMeta, Name: a.Name, Description: a.Description, Variables: v.Variables, Documents: docs, Contents: text})
		}
	}
	return result, source, nil
}

func builtinMarketDocuments(files fs.FS, docs map[string]marketplace.Document) (map[string]string, error) {
	out := map[string]string{}
	for _, doc := range docs {
		for _, f := range doc.Files {
			if !marketplace.ValidPath(f.Path) {
				return nil, fmt.Errorf("invalid builtin path")
			}
			b, e := fs.ReadFile(files, "builtin/"+f.Path)
			if e != nil {
				return nil, e
			}
			out[f.Path] = string(b)
		}
	}
	return out, nil
}
