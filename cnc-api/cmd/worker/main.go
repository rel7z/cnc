package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	cnc "github.com/fahrel/cnc"
)

func main() {
	configPath := flag.String("config", "worker_config.json", "Path to worker config file")
	flag.Parse()

	config, err := cnc.LoadWorkerConfig(*configPath)
	if err != nil {
		log.Printf("Config file not found or invalid (%v), using defaults", err)
		config = cnc.DefaultWorkerConfig()
	}

	worker := cnc.NewWorkerAgent(config)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down worker...")
		worker.Stop()
		os.Exit(0)
	}()

	fmt.Println("CNC Worker")
	fmt.Printf("  ID       : %s\n", config.WorkerID)
	fmt.Printf("  Server   : %s\n", config.ServerAddr)
	fmt.Printf("  MaxTasks : %d\n", config.MaxTasks)
	fmt.Printf("  DataDir  : %s\n", config.DataDir)
	fmt.Println()

	if err := worker.Start(); err != nil {
		log.Fatalf("Worker error: %v", err)
	}
}
