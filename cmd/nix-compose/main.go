package main

import (
	_ "embed"

	"github.com/systemstart/nix-compose/pkg/cli"
)

// version is set at build time via -ldflags.
var version = "dev"

//go:embed SKILL.md
var skillMD string

func main() {
	cli.Version = version
	cli.AgentDocs = skillMD
	cli.Execute()
}
