package main

import (
	"fmt"
	"os"

	"github.com/winebarrel/pgls/internal/lsp"
)

func main() {
	if err := lsp.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
