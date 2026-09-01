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
	configPath := flag.String("config", "server_config.json", "Path to server config file")
	flag.Parse()

	var config *cnc.ServerConfig
	var err error

	if _, statErr := os.Stat(*configPath); statErr == nil {
		config, err = cnc.LoadServerConfig(*configPath)
		if err != nil {
			log.Printf("Warning: failed to load config from %s: %v", *configPath, err)
			log.Println("Using default configuration")
			config = cnc.DefaultServerConfig()
		}
	} else {
		log.Printf("Config file %s not found, using defaults", *configPath)
		config = cnc.DefaultServerConfig()
	}

	server := cnc.NewServer(config)

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("\nReceived shutdown signal")
		server.Stop()
		os.Exit(0)
	}()

	fmt.Println("========================================")
	fmt.Println("     CNC Server")
	fmt.Println("========================================")
	fmt.Printf("HTTP API:    %s\n", config.HTTPAddr)
	fmt.Printf("TCP Server:  %s\n", config.TCPAddr)
	fmt.Printf("Data Dir:    %s\n", config.DataDir)
	fmt.Printf("Max Retries: %d\n", config.MaxRetries)
	fmt.Println("========================================")
	fmt.Println()

	if err := server.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
