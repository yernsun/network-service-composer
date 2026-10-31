package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestMaterialsRequireExplicitPrivatePasswordAndRetainPartialEvidence(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "short", "long", "invalid-controller-id", "invalid-pool-id", "invalid-gateway-id"} {
		t.Run(scenario, func(t *testing.T) {
			i, _ := installerFixture(t, 1)
			switch scenario {
			case "valid", "missing", "short", "long":
				i.Config.AdminPasswordFile = filepath.Join(t.TempDir(), "admin")
				data := "isolated-private-initial-password"
				if scenario == "short" {
					data = "short"
				}
				if scenario == "long" {
					data = strings.Repeat("x", 73)
				}
				if scenario != "missing" {
					os.WriteFile(i.Config.AdminPasswordFile, []byte(data), 0600)
				}
			case "invalid-controller-id":
				v := i.Record.Nodes["one"]
				v.InstanceID = "invalid/identity"
				i.Record.Nodes["one"] = v
			case "invalid-pool-id":
				i.Record.PoolID = "invalid"
			case "invalid-gateway-id":
				v := i.Record.Nodes["one"]
				v.GatewayID = "invalid"
				i.Record.Nodes["one"] = v
			}
			err := i.makeMaterials()
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				value, _ := bootstrap.ReadPrivate(i.material("admin-password"))
				if string(value) != "isolated-private-initial-password" {
					t.Fatal("provided initial password was replaced")
				}
			} else {
				if err == nil {
					t.Fatal("invalid bootstrap authority accepted")
				}
				if _, err = i.Reconcile(t.Context(), bootstrap.Step{Key: "materials"}); err == nil {
					t.Fatal("partial key creation claimed as complete")
				}
				if err = i.makeMaterials(); err == nil {
					t.Fatal("partial private material overwritten")
				}
			}
		})
	}
}

func TestProvisioningArchivesRejectEscapingPathsAndInvalidEntries(t *testing.T) {
	for _, name := range []string{"/absolute", "../outside", "a/../outside", ".", "file\nname"} {
		if _, _, err := nodeArchive(map[string]nodeFile{name: {data: []byte("private"), mode: 0400}}); err == nil {
			t.Fatal("unsafe archive entry accepted", name)
		}
	}
	if _, _, err := nodeArchive(map[string]nodeFile{"invalid-entry": {data: []byte("private"), uid: -1, mode: 0400}}); err == nil {
		t.Fatal("unrepresentable archive entry accepted")
	}
	i, _ := installerFixture(t, 1)
	infrastructure(t, i)
	identity := i.Record.Nodes["one"]
	identity.SocketGID = "invalid"
	i.Record.Nodes["one"] = identity
	if _, err := i.nodeFiles(i.Config.Nodes[0]); err == nil {
		t.Fatal("invalid socket group used for provisioning")
	}
	identity.SocketGID = "900"
	i.Record.Nodes["one"] = identity
	before := i.Record.Resources["configure:one"]
	h := i.Hosts["one"].(*fakeHost)
	h.manifest = []byte(`{`)
	if err := i.checkNodeFiles(t.Context(), "one", before.SpecDigest); err == nil {
		t.Fatal("corrupt remote manifest accepted")
	}
	if _, err := i.selectedNode("external"); err == nil {
		t.Fatal("external node selected")
	}
}

func TestPrivateMaterialCreationNeverOverwritesFilesOrInventsMissingStateDirectories(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "original-private-key")
	original := []byte("isolated-original-key-evidence")
	os.WriteFile(path, original, 0600)
	if err := exclusive(path, []byte("replacement")); err == nil {
		t.Fatal("exclusive private key overwritten")
	}
	actual, _ := os.ReadFile(path)
	if string(actual) != string(original) {
		t.Fatal("failed key creation changed existing material")
	}
	if err := exclusive(filepath.Join(path, "child"), []byte("replacement")); err == nil {
		t.Fatal("non-directory key parent accepted")
	}
	for _, scenario := range []string{"missing-state", "state-is-file"} {
		i, _ := installerFixture(t, 1)
		i.Directory = filepath.Join(i.Directory, "unexpected")
		if scenario == "state-is-file" {
			os.WriteFile(i.Directory, original, 0600)
		}
		if err := i.makeMaterials(); err == nil {
			t.Fatal("invalid state directory silently replaced", scenario)
		}
	}
}
