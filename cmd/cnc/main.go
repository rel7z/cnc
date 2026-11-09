package main

import (
	"flag"
	"fmt"
	"os"

	cnc "github.com/fahrel/cnc"
)

func main() {
	mode := flag.String("mode", "cli", "Mode: cli, server, worker")
	flag.Parse()

	switch *mode {
	case "cli":
		cnc.RunCLI()
	case "server":
		server := cnc.NewServer(nil)
		fmt.Println("Starting CNC Server...")
		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	case "worker":
		worker := cnc.NewWorkerAgent(nil)
		fmt.Println("Starting Worker Agent...")
		if err := worker.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "Worker error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *mode)
		os.Exit(1)
	}
}