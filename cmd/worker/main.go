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

	var config *cnc.WorkerConfig
	var err error

	if _, statErr := os.Stat(*configPath); statErr == nil {
		config, err = cnc.LoadWorkerConfig(*configPath)
		if err != nil {
			log.Printf("Warning: failed to load config from %s: %v", *configPath, err)
			log.Println("Using default configuration")
			config = cnc.DefaultWorkerConfig()
		}
	} else {
		log.Printf("Config file %s not found, using defaults", *configPath)
		config = cnc.DefaultWorkerConfig()
	}

	worker := cnc.NewWorkerAgent(config)

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nReceived shutdown signal")
		worker.Stop()
		os.Exit(0)
	}()

	fmt.Println("========================================")
	fmt.Println("     CNC Worker")
	fmt.Println("========================================")
	fmt.Printf("Worker ID:     %s\n", config.WorkerID)
	fmt.Printf("Server:        %s\n", config.ServerAddr)
	fmt.Printf("Max Tasks:     %d\n", config.MaxTasks)
	fmt.Printf("Capabilities:  %v\n", config.Capabilities)
	fmt.Printf("Data Dir:      %s\n", config.DataDir)
	fmt.Println("========================================")
	fmt.Println()

	if err := worker.Start(); err != nil {
		log.Fatalf("Worker error: %v", err)
	}
}
