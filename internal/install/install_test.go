package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteScriptPutsTheAssetOnDisk(t *testing.T) {
	dir := t.TempDir()

	path, err := writeScript(dir)
	if err != nil {
		t.Fatalf("writeScript: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("wrote to %s, want a file in %s", path, dir)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(body), "agentic-obs-inbox") {
		t.Error("the written script is not the bridge")
	}
}

func TestWriteScriptOverwritesAnOlderCopy(t *testing.T) {
	dir := t.TempDir()

	path, err := writeScript(dir)
	if err != nil {
		t.Fatalf("first writeScript: %v", err)
	}
	if err := os.WriteFile(path, []byte("-- stale"), 0o644); err != nil {
		t.Fatalf("stale write: %v", err)
	}

	if _, err := writeScript(dir); err != nil {
		t.Fatalf("second writeScript: %v", err)
	}

	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "stale") {
		t.Error("an upgrade left the old script in place")
	}
}

// A dry run must not touch anything -- it is what a cautious user reaches for
// before letting this near their scene collections.
//
// RULING 1: a dry run must not write the bridge script to disk either. The
// script directory is captured in its own variable (rather than an inline
// t.TempDir()) so this test can check it after the run.
func TestDryRunChangesNothing(t *testing.T) {
	scenes := t.TempDir()
	scriptDir := t.TempDir()
	collection := filepath.Join(scenes, "Test.json")
	original := `{"name":"Test"}`
	if err := os.WriteFile(collection, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := installInto(scenes, scriptDir, Options{All: true, DryRun: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("report listed %d changes, want 1", len(report.Changed))
	}

	after, _ := os.ReadFile(collection)
	if string(after) != original {
		t.Errorf("a dry run modified the collection:\n%s", after)
	}
	if len(report.Backups) != 0 {
		t.Errorf("a dry run wrote %d backups", len(report.Backups))
	}

	// The report must still say where the script would go, so dry-run output
	// stays useful -- but the file itself must not exist.
	if report.ScriptPath == "" {
		t.Fatal("report.ScriptPath is empty; a dry run should still report where it would write")
	}
	if _, err := os.Stat(report.ScriptPath); !os.IsNotExist(err) {
		t.Errorf("a dry run wrote the script to %s", report.ScriptPath)
	}
}

func TestInstallBacksUpEveryCollectionItChanges(t *testing.T) {
	scenes := t.TempDir()
	for _, name := range []string{"One.json", "Two.json"} {
		if err := os.WriteFile(filepath.Join(scenes, name), []byte(`{"name":"x"}`), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Backups) != 2 {
		t.Fatalf("made %d backups for 2 collections", len(report.Backups))
	}
	for _, backup := range report.Backups {
		if _, err := os.Stat(backup); err != nil {
			t.Errorf("backup %s is missing: %v", backup, err)
		}
	}
}

func TestInstallSkipsNonCollectionFiles(t *testing.T) {
	scenes := t.TempDir()
	if err := os.WriteFile(filepath.Join(scenes, "Real.json"), []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scenes, "Real.json.bak-20260101-000000"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write backup: %v", err)
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("changed %d files, want 1 -- backups must not be treated as collections", len(report.Changed))
	}
}

func TestRemoveTakesTheScriptBackOut(t *testing.T) {
	scenes := t.TempDir()
	scriptDir := t.TempDir()
	collection := filepath.Join(scenes, "One.json")
	if err := os.WriteFile(collection, []byte(`{"name":"x"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := installInto(scenes, scriptDir, Options{All: true}); err != nil {
		t.Fatalf("install: %v", err)
	}
	report, err := installInto(scenes, scriptDir, Options{All: true, Remove: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(report.Changed) != 1 {
		t.Fatalf("removed from %d collections, want 1", len(report.Changed))
	}

	body, _ := os.ReadFile(collection)
	if strings.Contains(string(body), "agentic-obs-bridge.lua") {
		t.Error("the script is still registered after a remove")
	}
}
