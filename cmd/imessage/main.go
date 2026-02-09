package main

import (
	"fmt"
	"os"

	"github.com/danewalton/imessage-cli/internal/cli"
	"github.com/danewalton/imessage-cli/internal/database"
)

func main() {
	defer database.CloseDB()

	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
