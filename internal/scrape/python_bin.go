package scrape

import (
	"os/exec"
	"strings"
)

// resolvePythonBin keeps deployments portable when an environment requests a
// versioned interpreter (for example python3.11) that is not installed in the
// runtime image. Prefer the configured binary, then common unversioned names.
func resolvePythonBin(configured string) string {
	candidates := []string{strings.TrimSpace(configured), "python3", "python"}
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
