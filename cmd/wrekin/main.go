// Command wrekin is the Wrekin agent.
//
// This file is a placeholder so that the packaging (GoReleaser, the Nix
// flake, CI) can be exercised before the real agent lands. Track A replaces
// it with the cobra-based CLI.
package main

import "fmt"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fmt.Printf("wrekin %s\n", version)
}
