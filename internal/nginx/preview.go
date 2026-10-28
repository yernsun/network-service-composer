package nginx

import (
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SourceRange uses one-based inclusive lines in the emitted file. Provenance is
// captured at emission, including header copies inherited into child contexts.
type SourceRange struct {
	File       string `json:"file"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Scope      string `json:"scope"`
	ResourceID string `json:"resource_id"`
	InputLine  int    `json:"input_line"`
	Inherited  bool   `json:"inherited"`
}
type directiveOrigin struct {
	Directive
	Scope, ResourceID string
	Inherited         bool
}
type PreviewDocument struct {
	Content string            `json:"content"`
	Files   map[string]string `json:"files"`
	Ranges  []SourceRange     `json:"ranges"`
}

func snippetOrigins(scope, resource, content string) []directiveOrigin {
	directives, _ := ParseSnippet(scope, content)
	result := []directiveOrigin{}
	for _, d := range directives {
		result = append(result, directiveOrigin{Directive: d, Scope: scope, ResourceID: resource})
	}
	return result
}
func inheritOrigins(child, parent []directiveOrigin) []directiveOrigin {
	result := append([]directiveOrigin{}, child...)
	seen := map[string]bool{}
	for _, d := range child {
		if d.Name == "add_header" {
			seen[strings.ToLower(strings.Trim(d.Args[0], `"'`))] = true
		}
	}
	for _, d := range parent {
		if d.Name == "add_header" && !seen[strings.ToLower(strings.Trim(d.Args[0], `"'`))] {
			d.Inherited = true
			result = append(result, d)
		}
	}
	return result
}
func (r *Renderer) emitSnippet(out *strings.Builder, content string, origins []directiveOrigin) {
	if r.trace {
		line := strings.Count(out.String(), "\n") + 1
		for _, d := range origins {
			count := strings.Count("    "+d.Raw+"\n", "\n")
			r.ranges = append(r.ranges, SourceRange{File: r.currentFile, Start: line, End: line + count - 1, Scope: d.Scope, ResourceID: d.ResourceID, InputLine: d.Line, Inherited: d.Inherited})
			line += count
		}
	}
	out.WriteString(content)
}

// Preview expands only NSC-owned include paths. It never reads a certificate or
// an arbitrary include and uses the exact same renderer as production.
func (r *Renderer) Preview(snapshot *gatewayv2.Snapshot) (PreviewDocument, error) {
	result := PreviewDocument{Files: map[string]string{}, Ranges: []SourceRange{}}
	directory, e := os.MkdirTemp("", "nsc-preview-")
	if e != nil {
		return result, e
	}
	defer os.RemoveAll(directory)
	r.trace = true
	r.ranges = nil
	defer func() { r.trace = false; r.ranges = nil }()
	if e = r.Render(snapshot, directory); e != nil {
		return result, e
	}
	result.Files["nginx.conf"] = string(r.MainConfig("/run/nginx.pid"))
	e = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".conf") {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err == nil {
			result.Files[filepath.ToSlash(relative)] = string(raw)
		}
		return err
	})
	if e != nil {
		return result, e
	}
	result.Ranges = append(result.Ranges, r.ranges...)
	keys := []string{}
	for key := range result.Files {
		if key != "nginx.conf" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var out strings.Builder
	for _, line := range strings.SplitAfter(result.Files["nginx.conf"], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "include ") {
			out.WriteString(line)
			continue
		}
		for _, statement := range strings.Split(strings.TrimSpace(line), ";") {
			fields := strings.Fields(statement)
			if len(fields) != 2 || fields[0] != "include" {
				continue
			}
			for _, key := range keys {
				matches, _ := filepath.Match(fields[1], key)
				if !matches {
					continue
				}
				out.WriteString("  # " + key + "\n")
				offset := strings.Count(out.String(), "\n")
				out.WriteString(result.Files[key])
				for _, span := range r.ranges {
					if span.File == key {
						span.File = "complete.conf"
						span.Start += offset
						span.End += offset
						result.Ranges = append(result.Ranges, span)
					}
				}
			}
		}
	}
	result.Content = out.String()
	result.Files["complete.conf"] = result.Content
	return result, nil
}
