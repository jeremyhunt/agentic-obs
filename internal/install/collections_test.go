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
