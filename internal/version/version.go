package version

import (
	"encoding/json"
	"runtime/debug"
)

var (
	OrchestratorCommit = ""
	DrellaOSCommit     = ""
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

	if DrellaOSCommit != "" {
		info.Components["drellaos"] = Component{Commit: DrellaOSCommit}
	}

	return info
}

func (i Info) JSON() ([]byte, error) {
	return json.MarshalIndent(i, "", "  ")
}
