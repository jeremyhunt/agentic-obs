// Package install puts the bridge script where OBS will load it.
//
// OBS stores the script list per scene collection, in the same JSON file that
// holds every scene and source the user owns. That is why this package backs
// up before it writes and refuses to run while OBS is open: OBS rewrites the
// collection on save and would discard the edit, or worse, race it.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
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
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("could not write %s: %w", path, err)
	}
	return nil
}

// scriptList returns the existing entries, and whether modules existed.
func scriptList(doc map[string]interface{}) []interface{} {
	modules, _ := doc["modules"].(map[string]interface{})
	if modules == nil {
		return nil
	}
	list, _ := modules[scriptsKey].([]interface{})
	return list
}

func setScriptList(doc map[string]interface{}, list []interface{}) {
	modules, _ := doc["modules"].(map[string]interface{})
	if modules == nil {
		modules = map[string]interface{}{}
		doc["modules"] = modules
	}
	modules[scriptsKey] = list
}

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

	list := scriptList(doc)
	for _, entry := range list {
		if entryPath(entry) == scriptPath {
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

	list := scriptList(doc)
	kept := make([]interface{}, 0, len(list))
	removed := false
	for _, entry := range list {
		if entryPath(entry) == scriptPath {
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
