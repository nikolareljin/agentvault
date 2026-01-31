package main

import (
	"os"

	"github.com/nikolareljin/agentvault/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
