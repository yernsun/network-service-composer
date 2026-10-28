package core

import (
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/yernsun/network-service-composer/internal/marketplace"
)

type builtinReadBoundaryFS struct {
	fs.FS
	manifest []byte
	path     string
	failure  error
}

func (f builtinReadBoundaryFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, f.failure
	}
	if name == "builtin/market.yaml" && f.manifest != nil {
		return f.manifest, nil
	}
	return fs.ReadFile(f.FS, name)
}

func TestBuiltinMarketDistributionFailuresDoNotReturnPartialSnapshots(t *testing.T) {
	raw, e := builtinCatalogFiles.ReadFile("builtin/market.yaml")
	if e != nil {
		t.Fatal(e)
	}
	for _, stage := range []string{"missing manifest", "malformed manifest", "invalid metadata", "missing source document", "missing app document", "multiple baselines", "no manual", "valid distribution"} {
		t.Run(stage, func(t *testing.T) {
			manifest, e := marketplace.Decode(raw)
			if e != nil {
				t.Fatal(e)
			}
			failure := errors.New("builtin distribution read failed")
			files := builtinReadBoundaryFS{FS: builtinCatalogFiles, failure: failure}
			readFailure := false
			valid := false
			switch stage {
			case "missing manifest":
				files.path = "builtin/market.yaml"
				readFailure = true
			case "malformed manifest":
				files.manifest = []byte("{")
			case "invalid metadata":
				manifest.Marketplace.ID = "../outside"
			case "missing source document":
				for _, doc := range manifest.Marketplace.Docs {
					for _, file := range doc.Files {
						files.path = "builtin/" + file.Path
						break
					}
					break
				}
				readFailure = true
			case "missing app document":
				for _, file := range manifest.Apps[0].Versions[0].Files.Readme {
					files.path = "builtin/" + file.Path
					break
				}
				readFailure = true
			case "multiple baselines":
				v := manifest.Apps[0].Versions[0]
				v.Version = "additional-test-version"
				manifest.Apps[0].Versions = append(manifest.Apps[0].Versions, v)
			case "no manual":
				manifest.Apps[0].Versions[0].Files.Manuals = nil
				valid = true
			case "valid distribution":
				valid = true
			}
			if files.manifest == nil {
				files.manifest, e = json.Marshal(manifest)
				if e != nil {
					t.Fatal(e)
				}
			}
			snapshots, source, e := builtinMarketSnapshotsFrom(files)
			if !valid {
				if e == nil || snapshots != nil || source != "" {
					t.Fatal("partial market distribution accepted", len(snapshots), e)
				}
				if readFailure && !errors.Is(e, failure) {
					t.Fatal("distribution read cause hidden", e)
				}
				return
			}
			if e != nil || len(snapshots) != builtinSnapshotCount(manifest) {
				t.Fatal("valid embedded catalog lost apps", e)
			}
			var metadata marketplace.Snapshot
			e = json.Unmarshal([]byte(source), &metadata)
			if e != nil || metadata.Metadata.Endpoint != "" || metadata.Metadata.ID != manifest.Marketplace.ID {
				t.Fatal("built-in source claimed remote publishing endpoint", e)
			}
			for _, app := range manifest.Apps {
				var snapshot marketplace.Snapshot
				e := json.Unmarshal([]byte(snapshots[app.ID]), &snapshot)
				if e != nil {
					t.Fatal(e)
				}
				for locale, file := range app.Versions[0].Files.Readme {
					want, e := builtinCatalogFiles.ReadFile("builtin/" + file.Path)
					if e != nil || snapshot.Documents["readme"].Files[locale].Path != file.Path || snapshot.Contents[file.Path] != string(want) {
						t.Fatal("localized document changed or missing", app.ID, locale, e)
					}
				}
			}
		})
	}
}

func TestBuiltinDocumentReaderConfinesPathsAndPreservesBytes(t *testing.T) {
	for _, path := range []string{"../outside", "/outside", "folder/../../outside", "missing.md", "readme.md"} {
		t.Run(path, func(t *testing.T) {
			files := fstest.MapFS{"builtin/readme.md": &fstest.MapFile{Data: []byte("# Original\n\nРусский / Español / 中文\n")}}
			docs := map[string]marketplace.Document{"manual": {Files: map[string]marketplace.File{"en": {Path: path}}}}
			content, e := builtinMarketDocuments(files, docs)
			if path == "readme.md" {
				if e != nil || content[path] != string(files["builtin/readme.md"].Data) {
					t.Fatal("document bytes altered", e)
				}
			} else if e == nil || content != nil {
				t.Fatal("invalid/missing asset accepted", e)
			}
		})
	}
	content, e := builtinMarketDocuments(fstest.MapFS{}, nil)
	if e != nil || len(content) != 0 {
		t.Fatal("optional empty documentation failed", e)
	}
}

func builtinSnapshotCount(m marketplace.Manifest) int {
	n := 0
	for _, a := range m.Apps {
		n += len(a.Versions)
	}
	return n
}
