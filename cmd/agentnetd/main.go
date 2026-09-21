// Command agentnetd - AgentNet daemon: local coordination service for agents.
package main

import (
	"os"

	"dorylinae/internal/version"
)

func main() {
	os.Exit(version.Main("agentnetd", "AgentNet daemon: local coordination service for agents.", os.Args[1:], os.Stdout, os.Stderr))
}
