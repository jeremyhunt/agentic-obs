// Package install puts the bridge script where OBS will load it.
//
// OBS stores the script list per scene collection, in the same JSON file that
// holds every scene and source the user owns. AddScript and RemoveScript modify
// this file in place. Callers are responsible for backing up the collection
// before editing and for refusing to edit while OBS is open (which would race
// the collection file).
//
// Note: every write reformats and re-indents the whole file and alphabetizes
// object keys (though array order is preserved), so diffs will show the entire
// file as changed even though only the scripts list was touched. The backup
// captures the full original state for recovery.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// scriptsKey is where the frontend-tools plugin keeps the list.
const scriptsKey = "scripts-tool"

// readCollection parses a collection with numbers left as json.Number.
//
// Decoding into interface{} would turn every number into a float64, and
// scene-item IDs are int64: anything past 2^53 would come back a different
// number and be written back corrupted. UseNumber keeps the original text.
func readCollection(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var doc map[string]interface{}
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	// Normalize null document to empty map
	if doc == nil {
		doc = make(map[string]interface{})
	}
	return doc, nil
}

func writeCollection(path string, doc map[string]interface{}) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("could not encode %s: %w", path, err)
	}

	// Get the original file's mode to preserve permissions across rename.
	// os.CreateTemp always creates at 0600, and rename carries source permissions
	// to destination, so we must restore the original before rename.
	var originalMode os.FileMode = 0o644
	if stat, err := os.Stat(path); err == nil {
		originalMode = stat.Mode()
	}

	// Write to a temp file in the same directory, then atomic rename.
	// This ensures the collection is either fully old or fully new, never half.
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, ".tmp-collection-*.json")
	if err != nil {
		return fmt.Errorf("could not create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file if anything fails before rename.
	defer func() {
		if _, err := os.Stat(tmpPath); err == nil {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(buf.Bytes()); err != nil {
		tmpFile.Close()
		return fmt.Errorf("could not write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("could not close temp file: %w", err)
	}

	// Restore the original file's permissions before rename.
	if err := os.Chmod(tmpPath, originalMode); err != nil {
		return fmt.Errorf("could not chmod temp file: %w", err)
	}

	// Atomic rename. Within a volume, rename is atomic: the collection is
	// either fully old bytes or fully new bytes, never corrupted partial state.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("could not rename temp file to %s: %w", path, err)
	}
	return nil
}

// scriptList returns the existing entries. If modules or scripts-tool is
// present but not the type it should be (and not null), it returns an error
// rather than silently discarding the value. Null is treated as absent (empty
// list).
func scriptList(doc map[string]interface{}) ([]interface{}, error) {
	modulesValue, hasModules := doc["modules"]
	// Missing or null: there is nothing to preserve, and setScriptList will
	// create the object.
	if !hasModules || modulesValue == nil {
		return nil, nil
	}

	// Present but not an object. Falling through here would hand
	// setScriptList a nil map, which replaces whatever this was with a fresh
	// object -- the same silent data loss already refused one level down for
	// scripts-tool.
	modules, ok := modulesValue.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("modules must be an object, got %T", modulesValue)
	}

	scriptsTool, exists := modules[scriptsKey]
	// Treat missing key or null value as empty list
	if !exists || scriptsTool == nil {
		return nil, nil
	}

	// If it exists and is non-nil, it must be an array. Don't silently discard.
	list, ok := scriptsTool.([]interface{})
	if !ok {
		return nil, fmt.Errorf("modules.%s must be an array, got %T", scriptsKey, scriptsTool)
	}
	return list, nil
}

func setScriptList(doc map[string]interface{}, list []interface{}) {
	modules, _ := doc["modules"].(map[string]interface{})
	if modules == nil {
		modules = map[string]interface{}{}
		doc["modules"] = modules
	}
	modules[scriptsKey] = list
}

// entryPath reads one script entry's path, exactly as the collection stores it.
//
// Callers compare it through obsScriptPath rather than raw. An entry written by
// an agentic-obs from before the slash fix holds backslashes, and one written
// by OBS's own Scripts dialog holds forward slashes; comparing raw means
// uninstall silently misses the stale entry and the next install adds a second
// one beside it. Only the stored side needs normalising -- installInto is the
// only caller, and it hands both functions an already-normalised scriptPath.
func entryPath(entry interface{}) string {
	obj, _ := entry.(map[string]interface{})
	if obj == nil {
		return ""
	}
	path, _ := obj["path"].(string)
	return path
}

// Backup copies path beside itself with a timestamp, and returns the copy's
// name.
func Backup(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("could not read %s to back it up: %w", path, err)
	}

	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return "", fmt.Errorf("could not write the backup %s: %w", backup, err)
	}
	return backup, nil
}

// AddScript registers scriptPath with the collection, reporting whether
// anything changed. Adding a script already present is a no-op, so upgrades
// can re-run it.
func AddScript(collectionPath, scriptPath string) (bool, error) {
	doc, err := readCollection(collectionPath)
	if err != nil {
		return false, err
	}

	list, err := scriptList(doc)
	if err != nil {
		return false, err
	}

	for _, entry := range list {
		if obsScriptPath(entryPath(entry)) == scriptPath {
			return false, nil
		}
	}

	list = append(list, map[string]interface{}{
		"path":     scriptPath,
		"settings": map[string]interface{}{},
	})
	setScriptList(doc, list)

	if err := writeCollection(collectionPath, doc); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveScript drops scriptPath from the collection, leaving every other entry
// alone.
func RemoveScript(collectionPath, scriptPath string) (bool, error) {
	doc, err := readCollection(collectionPath)
	if err != nil {
		return false, err
	}

	list, err := scriptList(doc)
	if err != nil {
		return false, err
	}

	kept := make([]interface{}, 0, len(list))
	removed := false
	for _, entry := range list {
		if obsScriptPath(entryPath(entry)) == scriptPath {
			removed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !removed {
		return false, nil
	}

	setScriptList(doc, kept)
	if err := writeCollection(collectionPath, doc); err != nil {
		return false, err
	}
	return true, nil
}
