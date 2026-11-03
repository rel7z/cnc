package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	cnc "github.com/fahrel/cnc"
	cnccluster "github.com/user/cnc-processor/cluster"
)

func main() {
	configPath := flag.String("config", "worker_config.json", "Path to worker config file")
	drainPID := flag.Int("drain-stop", 0, "Safely stop this worker PID after its tasks finish (systemd ExecStop)")
	drainSupport := flag.Bool("drain-support", false, "Report support for safe worker shutdown")
	prepare := flag.String("prepare-workspaces", "", "Copy registered bundle versions into stable workspaces without running programs (comma separated)")
	flag.Parse()
	if *drainSupport {
		fmt.Println(`{"drain_stop":true}`)
		return
	}

	config, err := cnc.LoadWorkerConfig(*configPath)
	if err != nil {
		if *drainPID != 0 {
			holdStop(*drainPID, err)
			return
		}
		log.Printf("Config file not found or invalid (%v), using defaults", err)
		config = cnc.DefaultWorkerConfig()
	}
	if *drainPID != 0 {
		if err := cnc.DrainWorkerProcess(context.Background(), *drainPID, config.DataDir); err != nil {
			holdStop(*drainPID, err)
		}
		return
	}
	if *prepare != "" {
		if err != nil {
			log.Fatal("Cannot prepare workspaces without a valid worker configuration")
		}
		root, err := filepath.Abs(filepath.Join(config.DataDir, "processor-v2"))
		if err != nil {
			log.Fatal(err)
		}
		runner := &cnccluster.Runner{Root: root, WorkspacesRoot: config.ProcessorWorkspaceRoot}
		for _, version := range strings.Split(*prepare, ",") {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			path, err := runner.PrepareWorkspace(ctx, version)
			cancel()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(path)
		}
		return
	}

	worker := cnc.NewWorkerAgent(config)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nDraining worker; waiting for active tasks and result delivery...")
		if err := worker.DrainAndStop(context.Background()); err != nil {
			log.Printf("Worker drain: %v", err)
		}
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

func holdStop(pid int, reason error) {
	// ExecStop failure would make systemd terminate an old worker anyway.
	// Refuse that unsafe fallback and leave its tasks running for inspection.
	if pid <= 1 {
		log.Fatal("Invalid worker PID for safe stop")
	}
	log.Printf("Safe shutdown deferred: %v; worker %d is left running", reason, pid)
	for syscall.Kill(pid, syscall.Signal(0)) == nil {
		time.Sleep(time.Second)
	}
}
