package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyFixture gives each test its own file to mangle.
func copyFixture(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("testdata", "collection.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestAddScriptAppendsWithoutDisturbingExistingEntries(t *testing.T) {
	path := copyFixture(t)

	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("AddScript: %v", err)
	}
	if !changed {
		t.Fatal("changed = false on a collection without the script")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 2 {
		t.Fatalf("got %d scripts, want 2", len(scripts))
	}
	if scripts[0] != "C:/existing/other.lua" {
		t.Errorf("the existing script moved or changed: %q", scripts[0])
	}
	if scripts[1] != "C:/agentic-obs/bridge.lua" {
		t.Errorf("scripts[1] = %q", scripts[1])
	}
}

func TestAddScriptIsIdempotent(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("first AddScript: %v", err)
	}
	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("second AddScript: %v", err)
	}
	if changed {
		t.Error("changed = true on the second add; upgrades re-run this")
	}
	if got := len(readScripts(t, path)); got != 2 {
		t.Errorf("got %d scripts after adding twice, want 2", got)
	}
}

// The test this file exists for. A naive decode into float64 turns
// 9007199254740993 into 9007199254740992 and silently corrupts a scene.
func TestAddScriptPreservesLargeIntegers(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(written), "9007199254740993") {
		t.Fatalf("the scene-item id was mangled; file now reads:\n%s", written)
	}
}

func TestAddScriptLeavesUnrelatedKeysAlone(t *testing.T) {
	path := copyFixture(t)

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	var got map[string]interface{}
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the file is no longer valid JSON: %v", err)
	}
	if got["current_scene"] != "Game" {
		t.Errorf("current_scene = %v", got["current_scene"])
	}
	if got["name"] != "Fixture" {
		t.Errorf("name = %v", got["name"])
	}
}

func TestAddScriptCreatesModulesWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bare.json")
	if err := os.WriteFile(path, []byte(`{"name":"Bare"}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}
	if got := readScripts(t, path); len(got) != 1 {
		t.Fatalf("got %d scripts, want 1", len(got))
	}
}

func TestRemoveScriptTakesOnlyOurs(t *testing.T) {
	path := copyFixture(t)
	if _, err := AddScript(path, "C:/agentic-obs/bridge.lua"); err != nil {
		t.Fatalf("AddScript: %v", err)
	}

	changed, err := RemoveScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("RemoveScript: %v", err)
	}
	if !changed {
		t.Error("changed = false when the script was present")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/existing/other.lua" {
		t.Fatalf("remove took the wrong entry: %v", scripts)
	}
}

func TestBackupCopiesTheFile(t *testing.T) {
	path := copyFixture(t)

	backup, err := Backup(path)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	original, _ := os.ReadFile(path)
	saved, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(original) != string(saved) {
		t.Error("the backup does not match the original")
	}
}

func readScripts(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc struct {
		Modules struct {
			Scripts []struct {
				Path string `json:"path"`
			} `json:"scripts-tool"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	out := make([]string, 0, len(doc.Modules.Scripts))
	for _, s := range doc.Modules.Scripts {
		out = append(out, s.Path)
	}
	return out
}

func TestAddScriptRejectsNonArrayScriptsTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	// Create a collection where scripts-tool is a string instead of array
	if err := os.WriteFile(path, []byte(`{
		"name": "Bad",
		"modules": {
			"scripts-tool": "not an array"
		}
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err == nil {
		t.Fatal("AddScript should error on non-array scripts-tool, got nil")
	}
	if changed {
		t.Error("changed should be false on error")
	}

	// Verify file is unchanged
	original, _ := os.ReadFile(path)
	if !strings.Contains(string(original), `"scripts-tool": "not an array"`) {
		t.Error("file was modified despite error")
	}
}

// The same defect the case above already refuses one level down. Falling
// through on a non-object "modules" handed setScriptList a nil map, which
// replaced whatever was there with a fresh object -- silently discarding it.
func TestAddScriptRejectsNonObjectModules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad-modules.json")
	if err := os.WriteFile(path, []byte(`{
		"name": "Bad",
		"modules": "not an object"
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err == nil {
		t.Fatal("AddScript should error on a non-object modules, got nil")
	}
	if changed {
		t.Error("changed should be false on error")
	}

	original, _ := os.ReadFile(path)
	if !strings.Contains(string(original), `"modules": "not an object"`) {
		t.Errorf("the value was discarded rather than preserved:\n%s", original)
	}
}

// Null modules is absent, not broken: there is nothing to lose, and
// setScriptList creates the object.
func TestAddScriptHandlesNullModules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "null-modules.json")
	if err := os.WriteFile(path, []byte(`{"name":"NullModules","modules":null}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("AddScript on null modules: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/agentic-obs/bridge.lua" {
		t.Fatalf("got %v, want [C:/agentic-obs/bridge.lua]", scripts)
	}
}

// An agentic-obs from before the slash fix wrote backslash entries on Windows.
// Comparing raw means uninstall-bridge cannot find one: it would leave the
// stale entry behind, and the next install would add a second beside it --
// the bridge loaded twice, two sources named agentic-obs-inbox, only one
// addressable. Both sides of the comparison must agree on separators.
func TestScriptEntriesMatchAcrossSeparators(t *testing.T) {
	const stored = `C:\Users\someone\AppData\Roaming\agentic-obs\bridge\agentic-obs-bridge.lua`
	const wanted = "C:/Users/someone/AppData/Roaming/agentic-obs/bridge/agentic-obs-bridge.lua"

	write := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "collection.json")
		doc, err := json.Marshal(map[string]interface{}{
			"name": "Pre-fix",
			"modules": map[string]interface{}{
				scriptsKey: []interface{}{
					map[string]interface{}{"path": stored, "settings": map[string]interface{}{}},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(path, doc, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	t.Run("RemoveScript finds a backslash entry", func(t *testing.T) {
		path := write(t)

		removed, err := RemoveScript(path, wanted)
		if err != nil {
			t.Fatalf("RemoveScript: %v", err)
		}
		if !removed {
			t.Fatal("uninstall could not find the entry it installed before the slash fix")
		}
		if scripts := readScripts(t, path); len(scripts) != 0 {
			t.Fatalf("the stale entry survived: %v", scripts)
		}
	})

	t.Run("AddScript treats a backslash entry as already present", func(t *testing.T) {
		path := write(t)

		changed, err := AddScript(path, wanted)
		if err != nil {
			t.Fatalf("AddScript: %v", err)
		}
		if changed {
			t.Error("install added a second entry for a file already registered")
		}
		if scripts := readScripts(t, path); len(scripts) != 1 {
			t.Fatalf("collection holds %d entries for one file: %v", len(scripts), scripts)
		}
	})
}

// Another script whose path merely resembles ours is still left alone: the
// normalisation must not widen what a removal matches.
func TestRemoveScriptStillLeavesOtherEntriesAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collection.json")
	if err := os.WriteFile(path, []byte(`{
		"modules": {
			"scripts-tool": [
				{"path": "C:/other/agentic-obs-bridge.lua", "settings": {}},
				{"path": "C:/agentic-obs/bridge.lua", "settings": {}}
			]
		}
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	removed, err := RemoveScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("RemoveScript: %v", err)
	}
	if !removed {
		t.Fatal("our own entry was not removed")
	}

	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/other/agentic-obs-bridge.lua" {
		t.Fatalf("got %v, want only the unrelated script left", scripts)
	}
}

func TestAddScriptHandlesNullDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "null.json")
	if err := os.WriteFile(path, []byte(`null`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Should not panic
	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("AddScript on null document: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}

	// Verify it now has the script
	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/agentic-obs/bridge.lua" {
		t.Fatalf("got %v, want [C:/agentic-obs/bridge.lua]", scripts)
	}
}

func TestAddScriptHandlesNullScriptsTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "null-scripts-tool.json")
	// Create a collection where scripts-tool is explicitly null
	if err := os.WriteFile(path, []byte(`{
		"name": "NullScriptsTool",
		"modules": {
			"scripts-tool": null
		}
	}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// null should be treated as absent/empty, not an error
	changed, err := AddScript(path, "C:/agentic-obs/bridge.lua")
	if err != nil {
		t.Fatalf("AddScript on null scripts-tool: %v", err)
	}
	if !changed {
		t.Fatal("changed should be true")
	}

	// Verify the script was added
	scripts := readScripts(t, path)
	if len(scripts) != 1 || scripts[0] != "C:/agentic-obs/bridge.lua" {
		t.Fatalf("got %v, want [C:/agentic-obs/bridge.lua]", scripts)
	}
}
