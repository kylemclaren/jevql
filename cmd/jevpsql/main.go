// Command jevpsql is a psql-shaped client that evaluates jev() calls with
// TypeSafe while the database only ever sees ordinary SQL.
package main

import (
	"os"

	"github.com/mclaren/jevpsql/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
