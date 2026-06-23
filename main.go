package main

import (
	"os"

	"claude-with-webhook/cmd"
)

func main() {
	os.Args[0] = "claude-webhook-server"

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
