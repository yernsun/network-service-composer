package agent

import (
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareDoesNotTrustIncompleteGenerationIdentity(t *testing.T) {
	for _, mode := range []string{"missing domains", "missing metadata", "missing renderer", "invalid model digest", "invalid rendered digest", "wrong main link"} {
		t.Run(mode, func(t *testing.T) {
			m := validationManagerFixture(t, "exit 0")
			old, e := os.Readlink(filepath.Join(m.root, "current"))
			if e != nil {
				t.Fatal(e)
			}
			directory := filepath.Join(m.root, old)
			state := GenerationState{PoolID: "pool", ConfigRevision: 7, ModelDigest: strings.Repeat("a", 64), RenderedDigest: strings.Repeat("b", 64), RendererVersion: nginxx.RendererVersion}
			switch mode {
			case "missing renderer":
				state.RendererVersion = ""
			case "invalid model digest":
				state.ModelDigest = "invalid"
			case "invalid rendered digest":
				state.RenderedDigest = "invalid"
			}
			if e = writeGenerationStateFile(directory, "generation.json", state); e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "missing domains":
				e = os.RemoveAll(filepath.Join(directory, "domains"))
			case "missing metadata":
				e = os.Remove(filepath.Join(directory, "generation.json"))
			case "wrong main link":
				e = os.Remove(filepath.Join(directory, generationMainConfigName))
				if e == nil {
					e = os.Symlink(filepath.Join(m.root, "foreign.conf"), filepath.Join(directory, generationMainConfigName))
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			got, e := m.Prepare()
			if e != nil || got.ConfigRevision != 0 || m.State().ConfigRevision != 0 {
				t.Fatal("unverified generation accepted as loaded", got, e)
			}
			if _, e = os.Stat(directory); e != nil {
				t.Fatal("untrusted prior generation erased instead of retained", e)
			}
		})
	}
}
func TestGenerationFilesystemErrorsNeverProduceConfirmedDigest(t *testing.T) {
	for _, mode := range []string{"old root loop", "old fragment loop", "candidate fragment loop", "old fragment directory", "state disk full"} {
		t.Run(mode, func(t *testing.T) {
			old, candidate := t.TempDir(), t.TempDir()
			if e := os.WriteFile(filepath.Join(candidate, "fragment.conf"), []byte("candidate"), 0600); e != nil {
				t.Fatal(e)
			}
			if mode == "state disk full" {
				if e := os.Symlink("/dev/full", filepath.Join(candidate, ".state.json-next")); e != nil {
					t.Fatal(e)
				}
				if e := writeGenerationState(candidate, GenerationState{ConfigRevision: 9}); e == nil {
					t.Fatal("failed metadata write reported success")
				}
				if _, e := os.Stat(filepath.Join(candidate, "state.json")); !os.IsNotExist(e) {
					t.Fatal("failed state became committed", e)
				}
				return
			}
			switch mode {
			case "old root loop":
				loop := filepath.Join(old, "loop")
				if e := os.Symlink("loop", loop); e != nil {
					t.Fatal(e)
				}
				old = loop
			case "old fragment loop":
				if e := os.Symlink("fragment.conf", filepath.Join(old, "fragment.conf")); e != nil {
					t.Fatal(e)
				}
			case "candidate fragment loop":
				if e := os.WriteFile(filepath.Join(old, "fragment.conf"), []byte("candidate"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Remove(filepath.Join(candidate, "fragment.conf")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink("fragment.conf", filepath.Join(candidate, "fragment.conf")); e != nil {
					t.Fatal(e)
				}
			case "old fragment directory":
				if e := os.Mkdir(filepath.Join(old, "fragment.conf"), 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e := reuseUnchangedFragments(old, candidate); e == nil {
				t.Fatal("unreadable prior or candidate content accepted")
			}
			if mode == "candidate fragment loop" {
				if digest, e := digestGeneration(candidate); e == nil || digest != "" {
					t.Fatal("unreadable candidate got digest", digest, e)
				}
			} else {
				raw, e := os.ReadFile(filepath.Join(candidate, "fragment.conf"))
				if e != nil || string(raw) != "candidate" {
					t.Fatal("reuse failure changed candidate", e)
				}
			}
		})
	}
}
func TestGenerationDigestRequiresCanonicalLowercaseSHA256(t *testing.T) {
	for _, v := range []string{"0", "9", "a", "f", "0123456789abcdef"} {
		text := strings.Repeat(v, 64/len(v))
		if !validDigest(text) {
			t.Fatal("valid digest rejected", text)
		}
	}
	for _, v := range []string{"/", ":", "@", "A", "F", "g", "é"} {
		if validDigest(strings.Repeat(v, 64)) {
			t.Fatal("noncanonical digest accepted", v)
		}
	}
}
