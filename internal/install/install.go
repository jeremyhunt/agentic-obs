package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	bridgescript "github.com/ironystock/agentic-obs/bridge"
)

// scriptName is the file written to disk and registered with OBS.
const scriptName = "agentic-obs-bridge.lua"

// Options controls one install or uninstall run.
type Options struct {
	// Collection names a single scene collection (without .json). Empty with
	// All false means the caller must choose.
	Collection string
	All        bool
	DryRun     bool
	Remove     bool
}

// Report says what happened, so the command can print it and a test can assert
// it.
type Report struct {
	ScriptPath string
	Changed    []string
	Skipped    []string
	Backups    []string
}

// CollectionsDir is where OBS keeps scene collections.
func CollectionsDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set, so OBS's config cannot be located")
		}
		return filepath.Join(appData, "obs-studio", "basic", "scenes"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "obs-studio", "basic", "scenes"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		return filepath.Join(config, "obs-studio", "basic", "scenes"), nil
	}
}

// ScriptDir is where the bridge script itself is written. It is deliberately
// not inside OBS's config: OBS rewrites that tree, and this file belongs to
// agentic-obs.
func ScriptDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(appData, "agentic-obs", "bridge"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "agentic-obs", "bridge"), nil
}

// Run performs an install or uninstall against the real OBS config.
func Run(opts Options) (Report, error) {
	scenes, err := CollectionsDir()
	if err != nil {
		return Report{}, err
	}
	scriptDir, err := ScriptDir()
	if err != nil {
		return Report{}, err
	}
	return installInto(scenes, scriptDir, opts)
}

// writeScript drops the embedded bridge into dir, replacing any older copy so
// that an upgrade is just a re-run.
//
// bridgescript.Script is compiled in, so unlike a file read there is no "the
// asset is missing" failure mode to handle here -- if it were missing, this
// package would not have built.
func writeScript(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("could not create %s: %w", dir, err)
	}

	path := filepath.Join(dir, scriptName)
	if err := os.WriteFile(path, bridgescript.Script, 0o644); err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, nil
}

// installInto is Run with its directories injected, which is what the tests
// drive.
func installInto(scenesDir, scriptDir string, opts Options) (Report, error) {
	report := Report{}

	// scriptPath is computed either way: AddScript/RemoveScript only ever
	// write this string into the collection's JSON, they never read the file
	// it names, so a dry run can reason about it without the file existing.
	scriptPath := filepath.Join(scriptDir, scriptName)

	// RULING 1: a dry run must not write anything, including the bridge
	// script itself -- "report what would change, write nothing" has to mean
	// nothing. Report.ScriptPath still carries the path so dry-run output
	// stays useful.
	if !opts.Remove && !opts.DryRun {
		written, err := writeScript(scriptDir)
		if err != nil {
			return report, err
		}
		scriptPath = written
	}
	report.ScriptPath = scriptPath

	collections, err := findCollections(scenesDir, opts)
	if err != nil {
		return report, err
	}

	for _, path := range collections {
		// A dry run still reports what it would do, by asking on a copy.
		if opts.DryRun {
			would, err := wouldChange(path, scriptPath, opts.Remove)
			if err != nil {
				return report, err
			}
			if would {
				report.Changed = append(report.Changed, path)
			} else {
				report.Skipped = append(report.Skipped, path)
			}
			continue
		}

		backup, err := Backup(path)
		if err != nil {
			return report, err
		}

		var changed bool
		if opts.Remove {
			changed, err = RemoveScript(path, scriptPath)
		} else {
			changed, err = AddScript(path, scriptPath)
		}
		if err != nil {
			// AddScript/RemoveScript made no write (confirmed by
			// TestAddScriptRejectsNonArrayScriptsTool), so path itself is
			// untouched -- but the backup just above already landed on disk.
			// Without this, a failed edit leaves an orphaned backup that
			// report.Backups never mentions.
			_ = os.Remove(backup)
			return report, err
		}

		if changed {
			report.Changed = append(report.Changed, path)
			report.Backups = append(report.Backups, backup)
		} else {
			report.Skipped = append(report.Skipped, path)
			_ = os.Remove(backup) // nothing changed, so the backup is litter
		}
	}

	return report, nil
}

// wouldChange answers the dry run by doing the edit on a temporary copy.
// Predicting it separately would mean two implementations that can disagree.
func wouldChange(collectionPath, scriptPath string, remove bool) (bool, error) {
	raw, err := os.ReadFile(collectionPath)
	if err != nil {
		return false, err
	}
	temp, err := os.CreateTemp("", "agentic-obs-dryrun-*.json")
	if err != nil {
		return false, err
	}
	defer os.Remove(temp.Name())

	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return false, err
	}
	temp.Close()

	if remove {
		return RemoveScript(temp.Name(), scriptPath)
	}
	return AddScript(temp.Name(), scriptPath)
}

// findCollections lists the scene collection files to operate on. Backups this
// tool made end in .bak-<timestamp>, so matching *.json exactly keeps them out.
func findCollections(scenesDir string, opts Options) ([]string, error) {
	if opts.Collection != "" {
		path := filepath.Join(scenesDir, opts.Collection+".json")
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("no scene collection named %q in %s", opts.Collection, scenesDir)
		}
		return []string{path}, nil
	}
	if !opts.All {
		return nil, fmt.Errorf("choose a collection with --collection NAME, or pass --all")
	}

	entries, err := os.ReadDir(scenesDir)
	if err != nil {
		return nil, fmt.Errorf("could not read %s: %w", scenesDir, err)
	}

	var found []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		found = append(found, filepath.Join(scenesDir, entry.Name()))
	}
	sort.Strings(found)

	if len(found) == 0 {
		return nil, fmt.Errorf("no scene collections found in %s", scenesDir)
	}
	return found, nil
}

// OBSIsRunning reports whether an OBS process is up.
//
// Installing while OBS runs is pointless and dangerous: OBS holds the
// collection in memory and rewrites it on save, so it would either discard the
// edit or race the write.
func OBSIsRunning() bool {
	names := map[string]bool{"obs64.exe": true, "obs.exe": true, "obs": true}

	entries, err := os.ReadDir("/proc")
	if err == nil { // Linux
		for _, entry := range entries {
			comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
			if err == nil && names[strings.TrimSpace(string(comm))] {
				return true
			}
		}
		return false
	}

	return obsIsRunningFallback(names)
}
