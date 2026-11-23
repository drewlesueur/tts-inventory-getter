package scrape

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolvePythonBin keeps deployments portable when an environment requests a
// versioned interpreter that is unavailable. Prefer the configured binary,
// then the project virtual environment, then common system names.
func resolvePythonBin(configured string) string {
	candidates := []string{strings.TrimSpace(configured)}
	if projectDir, err := os.Getwd(); err == nil {
		venv := filepath.Join(projectDir, ".venv")
		if info, err := os.Stat(filepath.Join(venv, "pyvenv.cfg")); err == nil && info.Mode().IsRegular() {
			candidates = append(candidates, filepath.Join(venv, "bin", "python"))
		}
	}
	candidates = append(candidates, "python3", "python")
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	return "python3"
}
