package version

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
)

var (
	OrchestratorCommit = ""
	OSReleasePaths     = []string{"/usr/lib/os-release", "/etc/os-release"}
)

type Component struct {
	Commit  string `json:"commit,omitempty"`
	Version string `json:"version,omitempty"`
	Repo    string `json:"repo,omitempty"`
}

type Info struct {
	Components map[string]Component `json:"components"`
}

func Get() Info {
	orch := Component{Commit: OrchestratorCommit}
	if orch.Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" && len(s.Value) >= 7 {
					orch.Commit = s.Value[:7]
					break
				}
			}
		}
	}
	orch.Repo = "drellahq/orchestrator"

	info := Info{
		Components: map[string]Component{
			"orchestrator": orch,
		},
	}

	rel := ParseOSRelease(OSReleasePaths)
	if rel["IMAGE_ID"] == "drellaos" {
		comp := Component{
			Commit: rel["BUILD_ID"],
			Repo:   "drellabot/drellaos",
		}
		if v := rel["IMAGE_VERSION"]; v != "" {
			comp.Version = v
		}
		info.Components["drellaos"] = comp
	}

	return info
}

func ParseOSRelease(paths []string) map[string]string {
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		result := make(map[string]string)
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			val = strings.Trim(val, `"'`)
			result[key] = val
		}
		return result
	}
	return make(map[string]string)
}

func (i Info) JSON() ([]byte, error) {
	return json.MarshalIndent(i, "", "  ")
}

func (i Info) WriteFile(path string) error {
	data, err := i.JSON()
	if err != nil {
		return fmt.Errorf("marshaling version info: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
