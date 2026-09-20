package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cnc "github.com/fahrel/cnc"
)

func main() {
	configPath := flag.String("config", "server_config.json", "Path to server config file")
	flag.Parse()

	for {
		config, err := cnc.LoadServerConfig(*configPath)
		if err != nil {
			log.Printf("Config file not found or invalid (%v), using defaults", err)
			config = cnc.DefaultServerConfig()
		}

		server := cnc.NewServer(config)

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\nShutting down...")
			server.Stop()
			os.Exit(0)
		}()

		fmt.Println("CNC Server")
		fmt.Printf("  HTTP : %s\n", config.HTTPAddr)
		fmt.Printf("  TCP  : %s\n", config.TCPAddr)
		fmt.Printf("  Data : %s\n", config.DataDir)
		fmt.Println()

		err = server.Start()
		signal.Stop(sigCh)

		if server.IsRestarting() || err == cnc.ErrRestartRequested {
			log.Println("[server] Restart requested, restarting server...")
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
		break
	}
}
