package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopiedJournalCannotAuthorizeAnotherDirectoryOnSameHost(t *testing.T) {
	cfg := testConfig()
	first := filepath.Join(t.TempDir(), "first")
	j, err := OpenJournal(first)
	if err != nil {
		t.Fatal(err)
	}
	_, err = j.Initialize(cfg, "same-machine")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(first, "installation.json"))
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second")
	clone, err := OpenJournal(second)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	defer j.Close()
	if err = AtomicPrivateFile(second, "installation.json", data); err != nil {
		t.Fatal(err)
	}
	if _, err = clone.Initialize(cfg, "same-machine"); err == nil {
		t.Fatal("copied journal established a second writer on the same host")
	}
}

func TestLegacyJournalAnchorIsEstablishedOnlyAfterIdentityChecks(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	journal, err := OpenJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	cfg := testConfig()
	record, err := journal.Initialize(cfg, "original-machine")
	if err != nil {
		t.Fatal(err)
	}
	id := record.ID
	record.MaintenanceAnchor = ""
	if err = journal.Save(&record); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Initialize(cfg, "different-machine"); err == nil {
		t.Fatal("legacy journal accepted a different host")
	}
	restored, err := journal.Initialize(cfg, "original-machine")
	if err != nil || restored.ID != id || restored.MaintenanceAnchor == "" {
		t.Fatal("legacy journal lost its original identity", err)
	}
}
