// Command wrekin is the Wrekin node agent and its command-line interface.
package main

import "github.com/plmercereau/wrekin/internal/cli"

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	cli.Execute(version)
}
