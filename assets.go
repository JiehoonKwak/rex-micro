// Package rexmicro holds files embedded into the rex-micro binary.
package rexmicro

import _ "embed"

// Skill is the agent skill shipped with rex-micro.
//
//go:embed skills/rex-micro/SKILL.md
var Skill string
