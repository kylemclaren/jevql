// Command jevql is semantic SQL for vanilla Postgres: a CLI, an HTTP and MCP
// node, and the engine behind the SDKs. jev() calls are evaluated with TypeSafe
// while the database only ever sees ordinary SQL.
package main

import (
	"os"

	"github.com/kylemclaren/jevql/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
