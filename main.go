package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/winebarrel/pgls/internal/lsp"
)

func main() {
	schemaDir := flag.String("schema", "", "directory containing PostgreSQL DDL .sql files")
	flag.Parse()

	if err := lsp.Run(*schemaDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
