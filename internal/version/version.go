package version

import (
	"encoding/json"
	"os"
	"runtime/debug"
	"strings"
)

var (
	OrchestratorCommit = ""
	DrellaOSCommit     = ""

	DrellaOSCommitFile = "/usr/lib/drellaos-commit"
)

type Component struct {
	Commit string `json:"commit,omitempty"`
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

	info := Info{
		Components: map[string]Component{
			"orchestrator": orch,
		},
	}

	commit := DrellaOSCommit
	if commit == "" {
		commit = readCommitFile(DrellaOSCommitFile)
	}
	if commit != "" {
		info.Components["drellaos"] = Component{Commit: commit}
	}

	return info
}

func readCommitFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (i Info) JSON() ([]byte, error) {
	return json.MarshalIndent(i, "", "  ")
}
