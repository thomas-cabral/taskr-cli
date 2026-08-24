// Package skills carries the agent skills this binary installs.
//
// They live here, in the repo that builds the CLI, because they document
// the CLI's own verbs and flags — and a skill that documents one binary
// while living beside another drifts the moment either moves. It has: the
// step verbs shipped here in August 2026 and the skill that was kept next
// to the server never mentioned them, so every agent reading it planned
// work with a plan feature it did not know existed.
//
// Embedding them is what makes `taskr skill install` work on a machine that
// has the binary and nothing else — no checkout, no network, no second
// download to keep in step with the first.
package skills

import (
	"embed"
	"io/fs"
)

//go:embed taskr/SKILL.md taskr-onboarding/SKILL.md
var files embed.FS

// Names are the skills this binary installs, in the order a reader meets
// them: the daily loop first, the onboarding path that only runs when a
// write is refused second.
var Names = []string{"taskr", "taskr-onboarding"}

// Body returns one skill's SKILL.md.
func Body(name string) ([]byte, error) {
	return fs.ReadFile(files, name+"/SKILL.md")
}
