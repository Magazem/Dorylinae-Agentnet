// Command relay - AgentNet relay: forwards encrypted traffic between daemons.
package main

import (
	"os"

	"github.com/Magazem/Dorylinae-Agentnet/internal/version"
)

func main() {
	os.Exit(version.Main("relay", "AgentNet relay: forwards encrypted traffic between daemons.", os.Args[1:], os.Stdout, os.Stderr))
}
