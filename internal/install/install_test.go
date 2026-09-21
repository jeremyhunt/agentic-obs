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

// Self-review finding: on --all, Backup(path) ran before AddScript/RemoveScript
// for every collection in the loop. If the edit then failed (as it does here,
// on a scripts-tool that is not an array), the loop returned immediately and
// left that collection's backup sitting on disk without ever recording it in
// report.Backups -- litter that grows on every retry of a broken collection.
func TestInstallDoesNotOrphanABackupWhenAnEditFails(t *testing.T) {
	scenes := t.TempDir()
	good := filepath.Join(scenes, "AAA_Good.json")
	bad := filepath.Join(scenes, "ZZZ_Bad.json")
	if err := os.WriteFile(good, []byte(`{"name":"Good"}`), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}
	// modules.scripts-tool is a string here, not an array, which AddScript
	// rejects rather than silently discarding (see collections_test.go).
	if err := os.WriteFile(bad, []byte(`{"name":"Bad","modules":{"scripts-tool":"not an array"}}`), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	// findCollections sorts, so Good is processed before Bad -- the run gets
	// partway through before the failure, which is the scenario that matters.
	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err == nil {
		t.Fatal("installInto succeeded despite a non-array scripts-tool")
	}

	if len(report.Changed) != 1 || report.Changed[0] != good {
		t.Fatalf("report.Changed = %v, want just [%s]", report.Changed, good)
	}
	if len(report.Backups) != 1 {
		t.Fatalf("report.Backups = %v, want exactly 1 (Good's)", report.Backups)
	}

	entries, err := os.ReadDir(scenes)
	if err != nil {
		t.Fatalf("read scenes dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "ZZZ_Bad.json.bak-") {
			t.Errorf("Bad's failed edit left an orphaned backup: %s", entry.Name())
		}
	}
}

// Coordinator review finding C: AddScript/RemoveScript's own errors (e.g.
// scriptList's "modules.scripts-tool must be an array") don't carry a path.
// On --all across several collections, an operator seeing the bare message
// had no way to tell which collection failed except by inferring it from
// alphabetical order. installInto must name it.
func TestInstallErrorNamesTheFailingCollection(t *testing.T) {
	scenes := t.TempDir()
	good := filepath.Join(scenes, "AAA_Good.json")
	bad := filepath.Join(scenes, "ZZZ_Bad.json")
	if err := os.WriteFile(good, []byte(`{"name":"Good"}`), 0o644); err != nil {
		t.Fatalf("write good: %v", err)
	}
	if err := os.WriteFile(bad, []byte(`{"name":"Bad","modules":{"scripts-tool":"not an array"}}`), 0o644); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	_, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err == nil {
		t.Fatal("installInto succeeded despite a non-array scripts-tool")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error %q does not name the failing collection %s", err.Error(), bad)
	}
}
