package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/winebarrel/pgls/internal/lsp"
	"github.com/winebarrel/pgls/schema"
)

func main() {
	schemaDir := flag.String("schema", "", "directory containing PostgreSQL DDL .sql files")
	flag.Parse()

	var s *schema.Schema
	if *schemaDir != "" {
		var err error
		s, err = schema.Load(*schemaDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if err := lsp.Run(s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
