package main

import (
	"os"

	"claude-with-webhook/cmd"
)

func main() {
	os.Args[0] = "claude-webhook-server"

	if len(os.Args) == 1 {
		os.Args = append(os.Args, "start")
	}

	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
