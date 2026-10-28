package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildOfficialMarketAndRejectMissingTranslation(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args = oldArgs; flag.CommandLine = oldFlags }()
	dir := t.TempDir()
	input := filepath.Join("..", "..", "internal", "core", "builtin", "market.yaml")
	runWith := func(path string) error {
		flag.CommandLine = flag.NewFlagSet("market-test", flag.ContinueOnError)
		os.Args = []string{"market-test", "--manifest", path, "--official", "--output", filepath.Join(dir, "out")}
		return run()
	}
	if e := runWith(input); e != nil {
		t.Fatal(e)
	}
	output, e := os.ReadFile(filepath.Join(dir, "out", "market.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(output), `"source_path"`) {
		t.Fatal("source paths required for relative document navigation")
	}
	// Copy source fixtures so a negative test can alter its manifest without mutating the repository.
	sourceRoot := filepath.Dir(input)
	copyRoot := filepath.Join(dir, "source")
	if e = os.CopyFS(copyRoot, os.DirFS(sourceRoot)); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(filepath.Join(copyRoot, "market.yaml"))
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	if e = json.Unmarshal(data, &m); e != nil {
		t.Fatal(e)
	}
	apps := m["apps"].([]any)
	v := apps[0].(map[string]any)["versions"].([]any)[0].(map[string]any)
	files := v["files"].(map[string]any)
	delete(files["readme"].(map[string]any), "ru")
	data, _ = json.Marshal(m)
	if e = os.WriteFile(filepath.Join(copyRoot, "market.yaml"), data, 0600); e != nil {
		t.Fatal(e)
	}
	if e = runWith(filepath.Join(copyRoot, "market.yaml")); e == nil {
		t.Fatal("missing official translation accepted")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "out", "market.yaml"))
	if string(after) != string(output) {
		t.Fatal("failed publication changed manifest")
	}
}
