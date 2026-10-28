// Package marketplace defines the portable, static marketplace format.
package marketplace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"

	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

type Text map[string]string

func (t *Text) UnmarshalYAML(n *yaml.Node) error {
	result := Text{}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag != "!!str" {
			return fmt.Errorf("localized text must be a string")
		}
		result["en"] = n.Value
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if key.Tag != "!!str" || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("localized text must map languages to strings")
			}
			if _, ok := result[key.Value]; ok {
				return fmt.Errorf("duplicate text locale %s", key.Value)
			}
			result[key.Value] = value.Value
		}
	default:
		return fmt.Errorf("localized text must be a string or language map")
	}
	*t = result
	return nil
}
func (t Text) Default(locale string) string {
	if s := t[locale]; s != "" {
		return s
	}
	return t["en"]
}

type File struct {
	SourcePath string `json:"source_path,omitempty" yaml:"source_path,omitempty"`
	Path       string `json:"path" yaml:"path"`
	SHA256     string `json:"sha256" yaml:"sha256"`
}

func (f *File) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Tag != "!!str" {
			return fmt.Errorf("file path must be a string")
		}
		*f = File{Path: n.Value}
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("file must be a path or descriptor")
	}
	fields := map[string]string{}
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Tag != "!!str" || v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return fmt.Errorf("file descriptor fields must be strings")
		}
		switch k.Value {
		case "path", "sha256", "source_path":
		default:
			return fmt.Errorf("unknown file field %s", k.Value)
		}
		if _, exists := fields[k.Value]; exists {
			return fmt.Errorf("duplicate file field %s", k.Value)
		}
		fields[k.Value] = v.Value
	}
	*f = File{Path: fields["path"], SHA256: fields["sha256"], SourcePath: fields["source_path"]}
	return nil
}

type Document struct {
	Title Text            `json:"title" yaml:"title"`
	Files map[string]File `json:"files" yaml:"files"`
	// Translation revisions explicitly track the source content a translation covers.
	Revision     string            `json:"revision,omitempty" yaml:"revision,omitempty"`
	Translations map[string]string `json:"translations,omitempty" yaml:"translations,omitempty"`
}
type Metadata struct {
	ID            string              `json:"id" yaml:"id"`
	Name          Text                `json:"name" yaml:"name"`
	Description   Text                `json:"description,omitempty" yaml:"description,omitempty"`
	Endpoint      string              `json:"endpoint" yaml:"endpoint"`
	Homepage      string              `json:"homepage,omitempty" yaml:"homepage,omitempty"`
	DefaultLocale string              `json:"default_locale" yaml:"default_locale"`
	Locales       []string            `json:"locales" yaml:"locales"`
	Docs          map[string]Document `json:"docs,omitempty" yaml:"docs,omitempty"`
}
type Source struct {
	Repository string `json:"repository" yaml:"repository"`
	Ref        string `json:"ref" yaml:"ref"`
	Directory  string `json:"directory,omitempty" yaml:"directory,omitempty"`
}
type Variable struct {
	Key         string   `json:"key" yaml:"key"`
	Group       string   `json:"group,omitempty" yaml:"group,omitempty"`
	Required    bool     `json:"required" yaml:"required"`
	Secret      bool     `json:"secret" yaml:"secret"`
	Generator   string   `json:"generator,omitempty" yaml:"generator,omitempty"`
	Default     string   `json:"default,omitempty" yaml:"default,omitempty"`
	Pattern     string   `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	MinLength   int      `json:"min_length,omitempty" yaml:"min_length,omitempty"`
	MaxLength   int      `json:"max_length,omitempty" yaml:"max_length,omitempty"`
	Choices     []string `json:"choices,omitempty" yaml:"choices,omitempty"`
	Description Text     `json:"description,omitempty" yaml:"description,omitempty"`
}
type Files struct {
	ReadmeRevision     string              `json:"readme_revision,omitempty" yaml:"readme_revision,omitempty"`
	ReadmeTranslations map[string]string   `json:"readme_translations,omitempty" yaml:"readme_translations,omitempty"`
	Swarm              File                `json:"swarm,omitempty" yaml:"swarm,omitempty"`
	Compose            File                `json:"compose" yaml:"compose"`
	EnvExample         File                `json:"env_example,omitempty" yaml:"env_example,omitempty"`
	Readme             map[string]File     `json:"readme,omitempty" yaml:"readme,omitempty"`
	Manuals            map[string]Document `json:"manuals,omitempty" yaml:"manuals,omitempty"`
	Assets             map[string]File     `json:"assets,omitempty" yaml:"assets,omitempty"`
}
type Version struct {
	Version            string              `json:"version" yaml:"version"`
	Source             *Source             `json:"source,omitempty" yaml:"source,omitempty"`
	Commit             string              `json:"commit,omitempty" yaml:"commit,omitempty"`
	Files              Files               `json:"files" yaml:"files"`
	Variables          []Variable          `json:"variables,omitempty" yaml:"variables,omitempty"`
	RecommendedDomains []RecommendedDomain `json:"recommended_domains,omitempty" yaml:"recommended_domains,omitempty"`
}

// Keep manifest fields typed so unknown keys and values that cannot be encoded
// in a stored JSON snapshot are rejected during decoding, before fetching files.
type RecommendedDomain struct {
	Service    string `json:"service" yaml:"service"`
	Host       string `json:"host,omitempty" yaml:"host,omitempty"`
	Path       string `json:"path,omitempty" yaml:"path,omitempty"`
	TargetPort int    `json:"target_port,omitempty" yaml:"target_port,omitempty"`
	TLSMode    string `json:"tls_mode,omitempty" yaml:"tls_mode,omitempty"`
}
type App struct {
	ID          string    `json:"id" yaml:"id"`
	Name        Text      `json:"name" yaml:"name"`
	Description Text      `json:"description,omitempty" yaml:"description,omitempty"`
	Versions    []Version `json:"versions" yaml:"versions"`
}
type Manifest struct {
	SchemaVersion int      `json:"schema_version" yaml:"schema_version"`
	Marketplace   Metadata `json:"marketplace" yaml:"marketplace"`
	Apps          []App    `json:"apps" yaml:"apps"`
}

// Snapshot contains only verified, local text. Reading documentation never fetches upstream content.
type Snapshot struct {
	Metadata       Metadata            `json:"metadata"`
	Name           Text                `json:"name,omitempty"`
	Description    Text                `json:"description,omitempty"`
	Variables      []Variable          `json:"variables,omitempty"`
	Documents      map[string]Document `json:"documents,omitempty"`
	Contents       map[string]string   `json:"contents,omitempty"`
	Assets         map[string]string   `json:"assets,omitempty"`
	ManifestSHA256 string              `json:"manifest_sha256,omitempty"`
	Commit         string              `json:"commit,omitempty"`
}

func Decode(data []byte) (Manifest, error) {
	var m Manifest
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("manifest must contain one document")
	}
	if m.SchemaVersion != 1 {
		return m, fmt.Errorf("unsupported manifest schema")
	}
	return m, nil
}
func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func JSON(v any) string         { b, _ := json.Marshal(v); return string(b) }
func ValidPath(p string) bool {
	u, e := url.Parse(p)
	return e == nil && p != "" && p != "." && !u.ForceQuery && strings.IndexFunc(p, unicode.IsControl) < 0 && !u.IsAbs() && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(p, "\\%\x00") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}
func (m Manifest) Validate(published bool) error {
	meta := m.Marketplace
	if !ValidPath(meta.ID) || strings.TrimSpace(meta.Name.Default(meta.DefaultLocale)) == "" || len(meta.Locales) == 0 {
		return fmt.Errorf("marketplace identity, name and locales are required")
	}
	found := false
	langs := map[string]bool{}
	for _, l := range meta.Locales {
		_, localeErr := language.Parse(l)
		if l == "" || strings.TrimSpace(l) != l || localeErr != nil || langs[l] {
			return fmt.Errorf("invalid or duplicate locale")
		}
		langs[l] = true
		if l == meta.DefaultLocale {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("default_locale must be listed in locales")
	}
	for _, raw := range []string{meta.Endpoint, meta.Homepage} {
		if raw == "" {
			continue
		}
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("marketplace URLs must use HTTPS without credentials, query or fragment")
		}
	}
	if meta.Endpoint == "" || !strings.HasSuffix(meta.Endpoint, "/") {
		return fmt.Errorf("endpoint must end with /")
	}
	check := func(f File, required bool) error {
		if f.SourcePath != "" && !ValidPath(f.SourcePath) {
			return fmt.Errorf("invalid original file path %q", f.SourcePath)
		}
		if f.Path == "" && !required {
			if f.SHA256 != "" || f.SourcePath != "" {
				return fmt.Errorf("file metadata requires a path")
			}
			return nil
		}
		if !ValidPath(f.Path) {
			return fmt.Errorf("invalid file path %q", f.Path)
		}
		if published {
			b, e := hex.DecodeString(f.SHA256)
			if e != nil || len(b) != 32 {
				return fmt.Errorf("missing SHA256 for %s", f.Path)
			}
		}
		return nil
	}
	docs := func(ds map[string]Document) error {
		for id, d := range ds {
			if !ValidPath(id) || len(d.Files) == 0 || d.Files[meta.DefaultLocale].Path == "" {
				return fmt.Errorf("document %s requires a default-language file", id)
			}
			for l, f := range d.Files {
				if !langs[l] {
					return fmt.Errorf("document %s has undeclared locale %s", id, l)
				}
				if err := check(f, true); err != nil {
					return err
				}
				if d.Revision != "" && d.Translations[l] != d.Revision {
					return fmt.Errorf("document %s translation %s is stale", id, l)
				}
			}
		}
		return nil
	}
	if err := docs(meta.Docs); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range m.Apps {
		if !ValidPath(a.ID) || seen[a.ID] {
			return fmt.Errorf("invalid or duplicate app %s", a.ID)
		}
		seen[a.ID] = true
		versions := map[string]bool{}
		for _, v := range a.Versions {
			if strings.TrimSpace(v.Version) == "" || v.Version != strings.TrimSpace(v.Version) || versions[v.Version] {
				return fmt.Errorf("duplicate or empty version for %s", a.ID)
			}
			versions[v.Version] = true
			if err := check(v.Files.Compose, true); err != nil {
				return err
			}
			if err := check(v.Files.Swarm, false); err != nil {
				return err
			}
			if err := check(v.Files.EnvExample, false); err != nil {
				return err
			}
			for l, f := range v.Files.Readme {
				if !langs[l] {
					return fmt.Errorf("undeclared README locale %s", l)
				}
				if err := check(f, true); err != nil {
					return err
				}
			}
			if len(v.Files.Readme) > 0 {
				if _, exists := v.Files.Manuals["readme"]; exists {
					return fmt.Errorf("readme is a reserved document ID")
				}
			}
			if len(v.Files.Readme) > 0 && v.Files.Readme[meta.DefaultLocale].Path == "" {
				return fmt.Errorf("README requires default language")
			}
			if err := docs(v.Files.Manuals); err != nil {
				return err
			}
			for key, f := range v.Files.Assets {
				if !ValidPath(key) {
					return fmt.Errorf("invalid asset key %s", key)
				}
				if err := check(f, true); err != nil {
					return err
				}
			}
			keys := map[string]bool{}
			for _, x := range v.Variables {
				if strings.TrimSpace(x.Key) == "" || keys[x.Key] || x.Secret && x.Default != "" {
					return fmt.Errorf("invalid variable/default %s", x.Key)
				}
				keys[x.Key] = true
			}
		}
	}
	return nil
}
