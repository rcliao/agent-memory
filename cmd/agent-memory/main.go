package main

import (
	"os"

	"github.com/rcliao/agent-memory/internal/cli"
)

func main() {
	if err := cli.RootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
