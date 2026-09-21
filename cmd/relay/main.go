// Command relay - AgentNet relay: forwards encrypted traffic between daemons.
package main

import (
	"os"

	"dorylinae/internal/version"
)

func main() {
	os.Exit(version.Main("relay", "AgentNet relay: forwards encrypted traffic between daemons.", os.Args[1:], os.Stdout, os.Stderr))
}
