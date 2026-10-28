// nsc-market compiles a maintained YAML manifest into a self-contained static market.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yernsun/network-service-composer/internal/i18n"
	"github.com/yernsun/network-service-composer/internal/marketplace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	input := flag.String("manifest", "internal/core/builtin/market.yaml", "maintained manifest")
	output := flag.String("output", "dist/market", "static output directory")
	endpoint := flag.String("endpoint", "", "deployment base HTTPS URL ending in /")
	homepage := flag.String("homepage", "", "market website")
	official := flag.Bool("official", false, "require documentation for every NSC locale")
	flag.Parse()
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	m, err := marketplace.Decode(data)
	if err != nil {
		return err
	}
	if *endpoint != "" {
		m.Marketplace.Endpoint = *endpoint
	}
	if *homepage != "" {
		m.Marketplace.Homepage = *homepage
	}
	if err = m.Validate(false); err != nil {
		return err
	}
	root, err := filepath.Abs(filepath.Dir(*input))
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if out == root {
		return fmt.Errorf("output must differ from input directory")
	}
	if err = os.MkdirAll(filepath.Join(out, "objects"), 0755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	caches := map[string]string{}
	defer func() {
		for _, d := range caches {
			_ = os.RemoveAll(d)
		}
	}()
	total := 0
	count := 0
	read := func(base, p string) ([]byte, error) {
		if !marketplace.ValidPath(p) {
			return nil, fmt.Errorf("invalid path %s", p)
		}
		target := filepath.Join(base, filepath.FromSlash(p))
		rel, e := filepath.Rel(base, target)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("path escapes source")
		}
		current := base
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, e := os.Lstat(current)
			if e != nil {
				return nil, e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("symlinks are not supported: %s", p)
			}
		}
		info, e := os.Stat(target)
		if e != nil {
			return nil, e
		}
		if !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return nil, fmt.Errorf("invalid or oversized file %s", p)
		}
		return os.ReadFile(target)
	}
	publish := func(base string, f *marketplace.File) error {
		if f.Path == "" {
			return nil
		}
		b, e := read(base, f.Path)
		if e != nil {
			return e
		}
		count++
		total += len(b)
		if count > 1000 || total > 32<<20 {
			return fmt.Errorf("market exceeds file/size limits")
		}
		digest := marketplace.Digest(b)
		p := "objects/" + digest
		if e = os.WriteFile(filepath.Join(out, p), b, 0644); e != nil {
			return e
		}
		*f = marketplace.File{Path: p, SHA256: digest, SourcePath: f.Path}
		return nil
	}
	checkDocs := func(ds map[string]marketplace.Document) error {
		if !*official {
			return nil
		}
		for id, d := range ds {
			if d.Revision == "" {
				return fmt.Errorf("official document %s requires revision and translations", id)
			}
			for _, l := range i18n.SupportedCodes() {
				if d.Files[l].Path == "" || d.Translations[l] != d.Revision {
					return fmt.Errorf("%s missing current translation %s", id, l)
				}
			}
		}
		return nil
	}
	docs := func(base string, ds map[string]marketplace.Document) error {
		if e := checkDocs(ds); e != nil {
			return e
		}
		for id, d := range ds {
			if *official {
				b, e := read(base, d.Files[m.Marketplace.DefaultLocale].Path)
				if e != nil {
					return e
				}
				if marketplace.Digest(b) != d.Revision {
					return fmt.Errorf("%s source revision changed; update translations", id)
				}
			}
			for l, f := range d.Files {
				if e := publish(base, &f); e != nil {
					return e
				}
				d.Files[l] = f
			}
			ds[id] = d
		}
		return nil
	}
	if *official {
		for _, l := range i18n.SupportedCodes() {
			found := false
			for _, ml := range m.Marketplace.Locales {
				if l == ml {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("official market missing locale %s", l)
			}
		}
	}
	if err = docs(root, m.Marketplace.Docs); err != nil {
		return err
	}
	for ai := range m.Apps {
		a := &m.Apps[ai]
		for vi := range a.Versions {
			v := &a.Versions[vi]
			base := root
			if v.Source != nil {
				src := v.Source
				parsed, parseErr := url.Parse(src.Repository)
				if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.ContainsAny(src.Repository, "\n\r") || strings.TrimSpace(src.Ref) == "" || strings.HasPrefix(src.Ref, "-") {
					return fmt.Errorf("invalid public Git source")
				}
				key := src.Repository + "\x00" + src.Ref
				dir, exists := caches[key]
				if !exists {
					dir, err = os.MkdirTemp("", "nsc-market-git-")
					if err != nil {
						return err
					}
					caches[key] = dir
					git := func(args ...string) ([]byte, error) {
						cmd := exec.CommandContext(ctx, "git", args...)
						cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
						b, e := cmd.CombinedOutput()
						if e != nil {
							return nil, fmt.Errorf("fetch market source failed: %w", e)
						}
						return b, nil
					}
					if _, err = git("-c", "core.hooksPath=/dev/null", "init", "--bare", dir); err != nil {
						return err
					}
					if _, err = git("-C", dir, "-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "http.followRedirects=false", "fetch", "--depth=1", "--no-tags", "--", src.Repository, src.Ref); err != nil {
						return err
					}
					b, e := git("-C", dir, "rev-parse", "FETCH_HEAD")
					if e != nil {
						return e
					}
					v.Commit = strings.TrimSpace(string(b))
					// checkout-index writes tracked files only, without hooks, filters or submodules.
					if _, err = git("-C", dir, "read-tree", "FETCH_HEAD"); err != nil {
						return err
					}
					tree := filepath.Join(dir, "tree")
					if err = os.Mkdir(tree, 0700); err != nil {
						return err
					}
					if _, err = git("-C", dir, "-c", "core.hooksPath=/dev/null", "checkout-index", "--all", "--prefix="+tree+"/"); err != nil {
						return err
					}
				} else {
					b, e := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "FETCH_HEAD").Output()
					if e != nil {
						return e
					}
					v.Commit = strings.TrimSpace(string(b))
				}
				base = filepath.Join(dir, "tree")
				if src.Directory != "" {
					if !marketplace.ValidPath(src.Directory) {
						return fmt.Errorf("invalid source directory")
					}
					base = filepath.Join(base, src.Directory)
					real, e := filepath.EvalSymlinks(base)
					if e != nil || real != base {
						return fmt.Errorf("invalid source directory")
					}
				}
				v.Source = nil
			}
			bundled := map[string]bool{}
			for _, f := range v.Files.Readme {
				bundled[f.Path] = true
			}
			for _, d := range v.Files.Manuals {
				for _, f := range d.Files {
					bundled[f.Path] = true
				}
			}
			for _, f := range v.Files.Assets {
				bundled[f.Path] = true
			}
			links := regexp.MustCompile(`!?\[[^\]]*\]\(([^\s)]+)\)`)
			for p := range bundled {
				if !strings.HasSuffix(strings.ToLower(p), ".md") {
					continue
				}
				b, e := read(base, p)
				if e != nil {
					return e
				}
				for _, match := range links.FindAllSubmatch(b, -1) {
					raw := string(match[1])
					u, e := url.Parse(raw)
					if e != nil {
						return fmt.Errorf("invalid documentation link")
					}
					if u.IsAbs() {
						if u.Scheme != "https" {
							return fmt.Errorf("unsupported documentation link %s", raw)
						}
						continue
					}
					if strings.HasPrefix(raw, "#") {
						continue
					}
					target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(p), u.Path)))
					if !bundled[target] {
						return fmt.Errorf("%s references unbundled file %s", p, target)
					}
				}
			}
			if err = publish(base, &v.Files.Compose); err != nil {
				return err
			}
			if err = publish(base, &v.Files.Swarm); err != nil {
				return err
			}
			if err = publish(base, &v.Files.EnvExample); err != nil {
				return err
			}
			if *official {
				b, e := read(base, v.Files.Readme[m.Marketplace.DefaultLocale].Path)
				if e != nil {
					return e
				}
				if marketplace.Digest(b) != v.Files.ReadmeRevision {
					return fmt.Errorf("%s README source revision changed", a.ID)
				}
				for _, l := range i18n.SupportedCodes() {
					if v.Files.ReadmeTranslations[l] != v.Files.ReadmeRevision {
						return fmt.Errorf("%s README translation stale: %s", a.ID, l)
					}
				}
				for _, l := range i18n.SupportedCodes() {
					if v.Files.Readme[l].Path == "" {
						return fmt.Errorf("%s README missing %s", a.ID, l)
					}
				}
				if len(v.Files.Manuals) == 0 {
					return fmt.Errorf("%s requires maintenance manual", a.ID)
				}
			}
			for l, f := range v.Files.Readme {
				if err = publish(base, &f); err != nil {
					return err
				}
				v.Files.Readme[l] = f
			}
			if err = docs(base, v.Files.Manuals); err != nil {
				return err
			}
			for k, f := range v.Files.Assets {
				if err = publish(base, &f); err != nil {
					return err
				}
				v.Files.Assets[k] = f
			}
		}
	}
	if err = m.Validate(true); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	temp := filepath.Join(out, "market.yaml.tmp")
	if err = os.WriteFile(temp, payload, 0644); err != nil {
		return err
	}
	if err = os.Rename(temp, filepath.Join(out, "market.yaml")); err != nil {
		return err
	}
	fmt.Printf("Published %d apps, %d files to %s\n", len(m.Apps), count, out)
	return nil
}
