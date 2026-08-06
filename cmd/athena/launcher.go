package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// launchTypeScriptTUI starts the Ink application without moving any engine
// responsibility into Node. The current Go executable is passed to the TUI,
// which starts it again in `engine` mode for the versioned stdio protocol.
//
// started is false when the TUI is not installed/built, allowing the caller to
// use the legacy Bubble Tea UI during the migration period.
func launchTypeScriptTUI() (started bool, err error) {
	entry, ok := findTUIEntry()
	if !ok {
		return false, nil
	}

	node, err := nodePath()
	if err != nil {
		return false, err
	}
	engine, err := os.Executable()
	if err != nil {
		return false, fmt.Errorf("find Athena executable: %w", err)
	}

	command := exec.Command(node, entry)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Env = append(os.Environ(), "ATHENA_ENGINE="+engine)
	return true, command.Run()
}

func findTUIEntry() (string, bool) {
	candidates := make([]string, 0, 3)
	if configured := os.Getenv("ATHENA_TUI_ENTRY"); configured != "" {
		candidates = append(candidates, configured)
	}
	if workingDir, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDir, "apps", "tui", "dist", "index.js"))
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "apps", "tui", "dist", "index.js"))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func nodePath() (string, error) {
	if configured := os.Getenv("ATHENA_NODE"); configured != "" {
		if _, err := exec.LookPath(configured); err != nil {
			return "", fmt.Errorf("configured ATHENA_NODE %q is unavailable: %w", configured, err)
		}
		return configured, nil
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("Node.js is required for the TypeScript TUI: %w", err)
	}
	return node, nil
}
