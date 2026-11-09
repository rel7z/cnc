package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/user/cnc-processor/api"
	"github.com/user/cnc-processor/cluster"
	"github.com/user/cnc-processor/internal/distributed"
	"github.com/user/cnc-processor/internal/store"
	"github.com/user/cnc-processor/internal/watcher"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "validate-retained" {
		// Run against a disposable database copy before stopping the live core.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		app := os.Getenv("CNC_APP_DIR")
		db := os.Getenv("CNC_PROCESSOR_DB")
		if app == "" || db == "" {
			log.Fatal("validate-retained requires explicit app and disposable database paths")
		}
		data, err := os.ReadFile(filepath.Join(app, "config.json"))
		if err != nil {
			log.Fatal(err)
		}
		var config cluster.Config
		if err = json.Unmarshal(data, &config); err != nil {
			log.Fatal(err)
		}
		pins := map[string]bool{}
		for _, id := range strings.Split(os.Getenv("CNC_PROCESSOR_PINNED_PROGRAMS"), ",") {
			if id != "" {
				pins[id] = true
			}
		}
		s, err := store.NewStore(db)
		if err != nil {
			log.Fatal(err)
		}
		defer s.Close()
		if err = s.InitSchema(); err != nil {
			log.Fatal(err)
		}
		c := distributed.Controller{Store: s, AppDir: app, PinnedPrograms: pins}
		if config, err = c.ResolveConfig(ctx, config); err != nil {
			log.Fatal(err)
		}
		for id := range config.Programs {
			if !pins[id] {
				log.Fatalf("retained validation refuses unpinned program %s", id)
			}
		}
		if err = c.Prepare(ctx); err != nil {
			log.Fatal(err)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]bool{"retained_config_valid": true})
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "rebalance-queued" {
		if len(os.Args) != 4 {
			log.Fatal("usage: cnc-processor rebalance-queued filename worker1,worker2,...")
		}
		db := os.Getenv("CNC_PROCESSOR_DB")
		if db == "" {
			db = "processor.db"
		}
		s, err := store.NewStore(db)
		if err != nil {
			log.Fatal(err)
		}
		defer s.Close()
		if err = s.InitSchema(); err != nil {
			log.Fatal(err)
		}
		result, err := s.RebalanceQueuedFile(context.Background(), os.Args[2], strings.Split(os.Args[3], ","))
		if err != nil {
			log.Fatal(err)
		}
		if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
			log.Fatal(err)
		}
		return
	}
	token := os.Getenv("CNC_PROCESSOR_TOKEN")
	if len(token) < 32 {
		log.Fatal("CNC_PROCESSOR_TOKEN must contain at least 32 characters")
	}
	// 1. Initialize SQLite Store
	dbPath := os.Getenv("CNC_PROCESSOR_DB")
	if dbPath == "" {
		dbPath = "processor.db"
	}
	s, err := store.NewStore(dbPath)
	if err != nil {
		log.Fatalf("Failed to open store: %v", err)
	}
	defer s.Close()

	// Initialize schema
	if err := s.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	if err := s.RecoverInterrupted(context.Background()); err != nil {
		log.Fatalf("Recovery failed: %v", err)
	}
	// Watcher
	ctxWatcher, cancelWatcher := context.WithCancel(context.Background())
	defer cancelWatcher()
	watchDir := os.Getenv("CNC_WATCH_DIR")
	if watchDir == "" {
		watchDir = "./merged"
	}
	capturer := &watcher.Capturer{Store: s, Dir: watchDir}
	appDir := os.Getenv("CNC_APP_DIR")
	if appDir == "" {
		appDir, _ = filepath.Abs(".")
	}
	configData, err := os.ReadFile(filepath.Join(appDir, "config.json"))
	if err != nil {
		log.Fatal(err)
	}
	var config cluster.Config
	if err = json.Unmarshal(configData, &config); err != nil {
		log.Fatal(err)
	}
	brokerURL := os.Getenv("CNC_PROCESSOR_BROKER_URL")
	workerURL := os.Getenv("CNC_PROCESSOR_WORKER_URL")
	if brokerURL == "" || workerURL == "" {
		log.Fatal("queue mode requires CNC_PROCESSOR_BROKER_URL and CNC_PROCESSOR_WORKER_URL")
	}
	controller := &distributed.Controller{Store: s, AppDir: appDir, WatchDir: watchDir, BrokerURL: brokerURL, WorkerURL: workerURL, Token: token}
	controller.PinnedPrograms = map[string]bool{}
	for _, id := range strings.Split(os.Getenv("CNC_PROCESSOR_PINNED_PROGRAMS"), ",") {
		if id != "" {
			controller.PinnedPrograms[id] = true
		}
	}
	if config, err = controller.ResolveConfig(ctxWatcher, config); err != nil {
		log.Fatal(err)
	}
	if err = controller.Prepare(ctxWatcher); err != nil {
		log.Fatal(err)
	}
	capturer.Routes = config.Routes
	capturer.SpreadWorkers = controller.CaptureSpreadWorkers
	go capturer.Start(ctxWatcher)
	schedulerDone := make(chan struct{})
	go func() {
		defer close(schedulerDone)
		controller.Start(ctxWatcher)
	}()

	// 2. Setup HTTP Server
	apiServer := &api.Server{Store: s, Capture: capturer.Capture, WatchDir: watchDir, AppDir: appDir}
	apiServer.ResetCheckpoints = capturer.ResetCheckpoints
	apiServer.ConfigurationStatus = controller.ConfigurationStatus
	apiServer.EditorList = controller.EditorList
	apiServer.EditorApply = func(ctx context.Context, d cluster.EditorDefinition, revision string) (any, error) {
		var result any
		err := capturer.Reconfigure(func() (map[string][]string, error) {
			var err error
			result, err = controller.EditorApply(ctx, d, revision)
			if err != nil {
				return nil, err
			}
			return controller.EditorRoutes(), nil
		})
		return result, err
	}
	if controller != nil {
		apiServer.WorkerAPI = http.HandlerFunc(controller.WorkerHandler)
		apiServer.ClusterStatus = controller.Status
	}

	srv := &http.Server{
		Addr:              "127.0.0.1:9191",
		Handler:           apiServer.Handler(token),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 3. Start server
	go func() {
		log.Println("Starting CNC Processor API on http://127.0.0.1:9191")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen error: %s\n", err)
		}
	}()

	// 4. Graceful Shutdown (Linux Process Safety)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	cancelWatcher()
	<-schedulerDone
	log.Println("Server exiting")
}
