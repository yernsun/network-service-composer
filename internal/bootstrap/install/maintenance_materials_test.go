package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

func TestRecoveryMaterialCannotUsePublicLinkedOrForeignOwnedDirectories(t *testing.T) {
	for _, scenario := range []string{"missing-metadata", "public-metadata", "linked-metadata", "public-offline", "linked-offline", "file-offline", "wrong-owner", "legacy-layout"} {
		t.Run(scenario, func(t *testing.T) {
			installer, _ := installerFixture(t, 1)
			switch scenario {
			case "missing-metadata":
				os.RemoveAll(installer.Directory)
			case "public-metadata":
				os.Chmod(installer.Directory, 0755)
			case "linked-metadata":
				target := installer.Directory
				installer.Directory = filepath.Join(t.TempDir(), "linked")
				os.Symlink(target, installer.Directory)
			case "public-offline":
				os.Mkdir(installer.offlineDir(), 0755)
				os.Chmod(installer.offlineDir(), 0755)
			case "linked-offline":
				os.Symlink(t.TempDir(), installer.offlineDir())
			case "file-offline":
				os.WriteFile(installer.offlineDir(), []byte("not-a-directory"), 0600)
			case "wrong-owner":
				installer.Record.MaintenanceUID++
			case "legacy-layout":
				installer.Record.MetadataLayout = 0
			}
			err := installer.ensureOfflineDirectory()
			if scenario == "legacy-layout" {
				if err != nil || installer.offlineDir() != installer.Directory {
					t.Fatal("legacy private layout cannot be read by its original executor", err)
				}
			} else if err == nil {
				t.Fatal("unsafe recovery directory accepted", scenario)
			}
		})
	}
}
func TestMaintenanceSecretCannotRegenerateLostIdentityOrPasswordMaterial(t *testing.T) {
	for _, filename := range []string{"postgres-password", "maintenance.key", "maintenance.crt", "ca.crt", "console.crt"} {
		t.Run(filename, func(t *testing.T) {
			installer, cluster := installerFixture(t, 1)
			if err := installer.makeMaterials(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(installer.material(filename)); err != nil {
				t.Fatal(err)
			}
			before := len(cluster.calls)
			if _, err := installer.createMaintenanceCredential(t.Context()); err == nil {
				t.Fatal("missing protected material gained a new runtime Secret")
			}
			if _, err := os.Stat(installer.material(filename)); !os.IsNotExist(err) {
				t.Fatal("lost credential was silently recreated")
			}
			for _, call := range cluster.calls[before:] {
				if strings.HasPrefix(call, "POST ") {
					t.Fatal("incomplete identity changed Swarm", call)
				}
			}
		})
	}
}
func TestInterruptedMaintenanceIdentityRetainsItsOriginalCAAndRequiresReview(t *testing.T) {
	installer, _ := installerFixture(t, 1)
	installer.Record.MaintenanceID = "invalid identity"
	if err := installer.makeMaterials(); err == nil {
		t.Fatal("untyped maintenance identity issued a credential")
	}
	before, err := os.ReadFile(installer.material("ca.key"))
	if err != nil {
		t.Fatal("partial installation did not retain its original CA", err)
	}
	installer.Record.MaintenanceID = "valid-maintenance"
	if err = installer.makeMaterials(); err == nil {
		t.Fatal("interruption silently replaced identities")
	}
	after, err := os.ReadFile(installer.material("ca.key"))
	if err != nil || bootstrap.Digest(before) != bootstrap.Digest(after) {
		t.Fatal("original CA changed during retry", err)
	}
	clear(before)
	clear(after)
}
