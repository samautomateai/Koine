// Package agentdir finds Koine's user-level directory, the counterpart of Pi's agent directory (~/.pi/agent).
package agentdir

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvVar overrides the directory. It maps Pi's PI_CODING_AGENT_DIR.
const EnvVar = "KOINE_AGENT_DIR"

// Of is the user-level directory: $KOINE_AGENT_DIR (a leading ~ is the home directory) or <home>/.koine. It is "" when neither is known.
func Of(home string) string {
	if d := os.Getenv(EnvVar); d != "" {
		if d == "~" || strings.HasPrefix(d, "~/") {
			d = filepath.Join(home, strings.TrimPrefix(d, "~"))
		}
		return d
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".koine")
}
