package install

import (
	"fmt"
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

// OBS's Lua loader splits a script path on '/' only -- obs_lua_script_create
// in shared/obs-scripting/obs-scripting-lua.c. The Python backend normalises
// backslashes; the Lua one does not. On Windows filepath.Join produced
// backslashes, so the collection got a path OBS cannot parse.
func TestObsScriptPathUsesForwardSlashes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`C:\Users\someone\AppData\Roaming\agentic-obs\bridge\agentic-obs-bridge.lua`,
			"C:/Users/someone/AppData/Roaming/agentic-obs/bridge/agentic-obs-bridge.lua"},
		{"/home/someone/.local/share/agentic-obs/bridge/agentic-obs-bridge.lua",
			"/home/someone/.local/share/agentic-obs/bridge/agentic-obs-bridge.lua"},
		{"C:/already/forward/agentic-obs-bridge.lua",
			"C:/already/forward/agentic-obs-bridge.lua"},
	}
	for _, tc := range cases {
		if got := obsScriptPath(tc.in); got != tc.want {
			t.Errorf("obsScriptPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The path installInto writes into a collection must carry no backslash, on
// any platform. Every other test in this package uses forward-slash literals,
// so none of them could see this.
func TestInstallWritesAForwardSlashPathIntoTheCollection(t *testing.T) {
	scenes := t.TempDir()
	collection := filepath.Join(scenes, "Test.json")
	if err := os.WriteFile(collection, []byte(`{"name":"Test"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := installInto(scenes, t.TempDir(), Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if strings.ContainsRune(report.ScriptPath, '\\') {
		t.Errorf("report.ScriptPath %q holds a backslash", report.ScriptPath)
	}

	scripts := readScripts(t, collection)
	if len(scripts) != 1 {
		t.Fatalf("collection holds %d scripts, want 1", len(scripts))
	}
	if strings.ContainsRune(scripts[0], '\\') {
		t.Errorf("the collection holds %q, which OBS's Lua loader cannot parse", scripts[0])
	}
}

// The concrete failure the slash mismatch caused: OBS's own Scripts dialog
// writes forward slashes, so a user who added the bridge by hand and then ran
// install-bridge failed the exact-string idempotency check and got a SECOND
// entry for the same file -- the bridge loaded twice, two sources named
// agentic-obs-inbox, only one of them addressable.
func TestInstallIsIdempotentAgainstAHandAddedEntry(t *testing.T) {
	scenes := t.TempDir()
	scriptDir := t.TempDir()
	handAdded := strings.ReplaceAll(filepath.Join(scriptDir, scriptName), `\`, "/")

	collection := filepath.Join(scenes, "Test.json")
	doc := fmt.Sprintf(`{"name":"Test","modules":{"scripts-tool":[{"path":%q,"settings":{}}]}}`, handAdded)
	if err := os.WriteFile(collection, []byte(doc), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	report, err := installInto(scenes, scriptDir, Options{All: true})
	if err != nil {
		t.Fatalf("installInto: %v", err)
	}
	if len(report.Changed) != 0 {
		t.Errorf("install changed %v, want nothing: the entry was already there", report.Changed)
	}

	scripts := readScripts(t, collection)
	if len(scripts) != 1 {
		t.Fatalf("collection holds %d entries for one file: %v", len(scripts), scripts)
	}
}

// A dry run's errors must name the collection, not the temporary copy it works
// on -- the same gap the write path already closed.
func TestDryRunErrorNamesTheCollection(t *testing.T) {
	scenes := t.TempDir()
	bad := filepath.Join(scenes, "Bad.json")
	if err := os.WriteFile(bad, []byte(`{"name":"Bad","modules":{"scripts-tool":"not an array"}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := installInto(scenes, t.TempDir(), Options{All: true, DryRun: true})
	if err == nil {
		t.Fatal("a dry run over a broken collection reported success")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error %q does not name the collection %s", err.Error(), bad)
	}
	if strings.Contains(err.Error(), "agentic-obs-dryrun-") {
		t.Errorf("error %q names the temporary copy instead of the collection", err.Error())
	}
}
