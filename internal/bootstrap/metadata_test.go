package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMetadataMountRejectsCredentialsUnknownFilesAndPathAliases(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "metadata")
	journal, err := OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	config := testConfig()
	record, err := journal.Initialize(config, "physical-node")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPlan(record, Init, "known", []Step{{Key: "verify"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(plan)
	if err = AtomicPrivateFile(directory, "plan.json", encoded); err != nil {
		t.Fatal(err)
	}
	if err = ValidateMetadataContents(directory, record); err != nil {
		t.Fatal("typed metadata was rejected", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(directory, alias); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{directory, filepath.Join(directory, "credential"), filepath.Join(alias, "nested", "credential")} {
		if ValidateMetadataReferences(directory, reference) == nil {
			t.Fatal("a credential path entered the runtime journal mount")
		}
	}
	if err = ValidateMetadataReferences(directory, directory+".offline/ca.key", ""); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"credential", "directory", "symlink", "trailing", "lock-data"} {
		t.Run(scenario, func(t *testing.T) {
			filename := filepath.Join(directory, "unexpected")
			switch scenario {
			case "directory":
				os.Mkdir(filename, 0700)
			case "symlink":
				os.Symlink(filepath.Join(directory, "plan.json"), filename)
			case "trailing":
				os.WriteFile(filename, append(append([]byte{}, encoded...), []byte("\nprivate data")...), 0600)
			case "lock-data":
				filename = filepath.Join(directory, "executor.lock")
				os.WriteFile(filename, []byte("private data"), 0600)
			default:
				os.WriteFile(filename, []byte("not metadata"), 0600)
			}
			if ValidateMetadataContents(directory, record) == nil {
				t.Fatal("non-metadata entered the executor mount")
			}
			if scenario == "lock-data" {
				os.Truncate(filename, 0)
			} else {
				os.RemoveAll(filename)
			}
		})
	}
}
func TestMissingJournalLockNeverCreatesAReplacementWriterFence(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "metadata")
	journal, err := OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Initialize(testConfig(), "physical-node"); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(directory, "executor.lock"))
	defer journal.Close()
	if other, err := OpenJournal(directory); err == nil {
		other.Close()
		t.Fatal("missing fence authorized another writer")
	}
	if _, err = os.Stat(filepath.Join(directory, "executor.lock")); !os.IsNotExist(err) {
		t.Fatal("lock was silently regenerated")
	}
	data, _ := os.ReadFile(filepath.Join(directory, "installation.json"))
	os.WriteFile(filepath.Join(directory, "installation.json"), append(data, []byte("private trailing content")...), 0600)
	if _, err = ReadJournal(directory); err == nil {
		t.Fatal("trailing data entered a typed journal")
	}
}

func TestExecutorMetadataAcceptsOnlyItsBoundPrivateDocuments(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "metadata")
	journal, err := OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	config := testConfig()
	record, err := journal.Initialize(config, "original-node")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "guided.json")
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filename, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = ValidateMetadataContents(directory, record); err != nil {
		t.Fatal("the exact guided input was rejected", err)
	}
	os.Remove(filename)
	for _, scenario := range []string{"foreign-record", "foreign-config", "foreign-plan", "changed-plan", "foreign-guided-input", "public-document", "empty-document"} {
		t.Run(scenario, func(t *testing.T) {
			var value any = record
			mode := os.FileMode(0600)
			switch scenario {
			case "foreign-record":
				copy := record
				copy.ID = "other-installation"
				value = copy
			case "foreign-config":
				copy := record
				copy.ConfigDigest = "different-input"
				value = copy
			case "foreign-plan", "changed-plan":
				plan, e := NewPlan(record, Verify, "verified", []Step{{Key: "verify"}}, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				if scenario == "foreign-plan" {
					plan.InstallationID = "other-installation"
				} else {
					plan.Hash = "unapproved-plan"
				}
				value = plan
			case "foreign-guided-input":
				copy := config
				copy.Name = "other-installation"
				value = copy
			case "public-document":
				mode = 0644
			}
			data, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "empty-document" {
				data = nil
			}
			if e = os.WriteFile(filename, data, mode); e != nil {
				t.Fatal(e)
			}
			if e = os.Chmod(filename, mode); e != nil {
				t.Fatal(e)
			}
			if ValidateMetadataContents(directory, record) == nil {
				t.Fatal("foreign or public metadata entered the worker mount")
			}
			if e = os.Remove(filename); e != nil {
				t.Fatal(e)
			}
		})
	}
	cycle := filepath.Join(t.TempDir(), "cycle")
	if err = os.Symlink(cycle, cycle); err != nil {
		t.Fatal(err)
	}
	if ValidateMetadataReferences(directory, cycle) == nil || ValidateMetadataReferences(cycle, filename) == nil {
		t.Fatal("a cyclic path bypassed the private-material boundary")
	}
	if ValidateMetadataContents(filepath.Join(directory, "missing"), record) == nil {
		t.Fatal("a missing metadata mount was accepted")
	}
}
