// Command agentnet - AgentNet CLI: talks to the local agentnetd daemon.
package main

import (
	"os"

	"dorylinae/internal/version"
)

func main() {
	os.Exit(version.Main("agentnet", "AgentNet CLI: talks to the local agentnetd daemon.", os.Args[1:], os.Stdout, os.Stderr))
}
