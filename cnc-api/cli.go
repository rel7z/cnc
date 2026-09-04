package cnc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// CLI is the command-line interface for interacting with a running CNC server.
type CLI struct {
	serverURL string
	client    *http.Client
}

func NewCLI() *CLI {
	return &CLI{
		serverURL: "http://localhost:8080",
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Execute wires up the cobra command tree and runs it.
func (c *CLI) Execute() error {
	root := &cobra.Command{
		Use:   "cnc",
		Short: "CNC — distribute a file across workers and run a command on each part",
		Long: `CNC splits an input file across connected workers and runs your command on each part.

Examples:
  cnc run targets.txt "node index.js {input}"
  cnc run hosts.txt "python scan.py {input}" --workers 5
  cnc run data.txt "grep pattern {input}" --watch
  cnc workers
  cnc jobs
  cnc status <job-id>`,
	}

	root.PersistentFlags().StringVar(&c.serverURL, "server", c.serverURL, "CNC server HTTP address")

	root.AddCommand(
		c.cmdServerStart(),
		c.cmdWorkerStart(),
		c.cmdRun(),
		c.cmdWorkers(),
		c.cmdJobs(),
		c.cmdStatus(),
	)

	return root.Execute()
}

// ── server start ──────────────────────────────────────────────────────────────

func (c *CLI) cmdServerStart() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Start the CNC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := LoadServerConfig("server_config.json")
			if err != nil {
				config = DefaultServerConfig()
			}
			fmt.Printf("CNC Server\n  HTTP : %s\n  TCP  : %s\n  Data : %s\n\n",
				config.HTTPAddr, config.TCPAddr, config.DataDir)
			srv := NewServer(config)
			return srv.Start()
		},
	}
}

// ── worker start ──────────────────────────────────────────────────────────────

func (c *CLI) cmdWorkerStart() *cobra.Command {
	var serverAddr string

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Start a worker agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := LoadWorkerConfig("worker_config.json")
			if err != nil {
				config = DefaultWorkerConfig()
			}
			// CLI flag overrides config file.
			if serverAddr != "" {
				config.ServerAddr = serverAddr
			}
			fmt.Printf("Worker %s\n  Server : %s\n  Slots  : %d\n\n",
				config.WorkerID, config.ServerAddr, config.MaxTasks)
			w := NewWorkerAgent(config)
			return w.Start()
		},
	}

	cmd.Flags().StringVarP(&serverAddr, "server", "s", "", "CNC server TCP address (e.g. 172.104.180.163:9090)")
	return cmd
}

// ── run ───────────────────────────────────────────────────────────────────────

func (c *CLI) cmdRun() *cobra.Command {
	var (
		workers    int
		timeoutSec int
		watch      bool
	)

	cmd := &cobra.Command{
		Use:   "run <file> <command>",
		Short: "Split a file across workers and run a command on each part",
		Long: `Split <file> into equal parts and run <command> on each part across connected workers.
Use {input} in your command as the placeholder for each chunk path.

Examples:
  cnc run targets.txt "node index.js {input}"
  cnc run hosts.txt "python scan.py {input}" --workers 5
  cnc run data.txt "grep -c pattern {input}" --workers 3 --watch`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.runJob(args[0], args[1], workers, timeoutSec, watch)
		},
	}

	cmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of parts to split into (default: all online workers)")
	cmd.Flags().IntVarP(&timeoutSec, "timeout", "t", DefaultTimeout, "Per-task timeout in seconds")
	cmd.Flags().BoolVar(&watch, "watch", false, "Stream progress until the job completes")

	return cmd
}

func (c *CLI) runJob(inputFile, command string, workers, timeoutSec int, watch bool) error {
	if timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}

	job := Job{
		Name:           filepath.Base(inputFile),
		Command:        command,
		InputFile:      inputFile,
		Workers:        workers,
		TimeoutSeconds: timeoutSec,
	}

	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}

	resp, err := c.client.Post(c.serverURL+"/api/jobs", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		var msg string
		json.NewDecoder(resp.Body).Decode(&msg) //nolint:errcheck
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, msg)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	jobID := result["job_id"]
	fmt.Printf("Job submitted: %s\n", jobID)
	fmt.Printf("  file:     %s\n", inputFile)
	fmt.Printf("  command:  %s\n", command)
	if workers > 0 {
		fmt.Printf("  workers:  %d\n", workers)
	} else {
		fmt.Printf("  workers:  auto (all online)\n")
	}

	if !watch {
		fmt.Printf("\nProgress: cnc status %s\n", jobID)
		return nil
	}

	fmt.Println()
	return c.watchJob(jobID)
}

func (c *CLI) watchJob(jobID string) error {
	for {
		time.Sleep(2 * time.Second)

		r, err := c.client.Get(c.serverURL + "/api/jobs/" + jobID)
		if err != nil {
			fmt.Printf("  poll error: %v\n", err)
			continue
		}

		var j Job
		decErr := json.NewDecoder(r.Body).Decode(&j)
		r.Body.Close()
		if decErr != nil {
			fmt.Printf("  decode error: %v\n", decErr)
			continue
		}

		pct := 0
		if j.TotalTasks > 0 {
			pct = (j.Completed + j.Failed) * 100 / j.TotalTasks
		}
		fmt.Printf("\r  [%s] %d/%d done  %d failed  %d%%   ",
			j.Status, j.Completed, j.TotalTasks, j.Failed, pct)

		if j.Status == "completed" || j.Status == "failed" || j.Status == "cancelled" {
			fmt.Println()
			fmt.Printf("Done: %s — %d ok, %d failed\n", j.Status, j.Completed, j.Failed)
			return nil
		}
	}
}

// ── workers ───────────────────────────────────────────────────────────────────

func (c *CLI) cmdWorkers() *cobra.Command {
	return &cobra.Command{
		Use:   "workers",
		Short: "List connected workers",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := c.client.Get(c.serverURL + "/api/workers")
			if err != nil {
				return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
			}
			defer resp.Body.Close()

			var workers []Worker
			if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
				return err
			}

			if len(workers) == 0 {
				fmt.Println("No workers connected.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tLOAD\tLAST SEEN")
			for _, w := range workers {
				fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%s\n",
					w.ID, w.Status, w.CurrentLoad, w.MaxTasks,
					w.LastSeen.Format("15:04:05"),
				)
			}
			return tw.Flush()
		},
	}
}

// ── jobs ──────────────────────────────────────────────────────────────────────

func (c *CLI) cmdJobs() *cobra.Command {
	return &cobra.Command{
		Use:   "jobs",
		Short: "List all jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := c.client.Get(c.serverURL + "/api/jobs")
			if err != nil {
				return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
			}
			defer resp.Body.Close()

			var jobs []Job
			if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
				return err
			}

			if len(jobs) == 0 {
				fmt.Println("No jobs yet.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tWORKERS\tDONE\tFAILED\tCREATED")
			for _, j := range jobs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
					j.ID, j.Name, j.Status, j.Workers,
					j.Completed, j.Failed,
					j.CreatedAt.Format("15:04:05"),
				)
			}
			return tw.Flush()
		},
	}
}

// ── status ────────────────────────────────────────────────────────────────────

func (c *CLI) cmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status [job-id]",
		Short: "Show cluster status, or detail for a specific job",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return c.jobStatus(args[0])
			}
			return c.clusterStatus()
		},
	}
}

func (c *CLI) jobStatus(jobID string) error {
	resp, err := c.client.Get(c.serverURL + "/api/jobs/" + jobID)
	if err != nil {
		return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("job %s not found", jobID)
	}

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return err
	}

	pct := 0
	if job.TotalTasks > 0 {
		pct = (job.Completed + job.Failed) * 100 / job.TotalTasks
	}

	fmt.Printf("Job:      %s\n", job.ID)
	fmt.Printf("Name:     %s\n", job.Name)
	fmt.Printf("Command:  %s\n", job.Command)
	fmt.Printf("File:     %s\n", job.InputFile)
	fmt.Printf("Workers:  %d\n", job.Workers)
	fmt.Printf("Status:   %s\n", job.Status)
	fmt.Printf("Progress: %d/%d (%d%%)\n", job.Completed+job.Failed, job.TotalTasks, pct)
	fmt.Printf("Done:     %d   Failed: %d\n", job.Completed, job.Failed)
	if job.StartedAt != nil {
		fmt.Printf("Started:  %s\n", job.StartedAt.Format("15:04:05"))
	}
	if job.CompletedAt != nil {
		fmt.Printf("Finished: %s\n", job.CompletedAt.Format("15:04:05"))
	}
	return nil
}

func (c *CLI) clusterStatus() error {
	resp, err := c.client.Get(c.serverURL + "/api/stats")
	if err != nil {
		return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	var stats map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "METRIC\tVALUE")
	keys := []string{
		"workers_online", "workers_total",
		"jobs_running", "jobs_total",
		"tasks_pending", "tasks_running", "tasks_completed", "tasks_failed",
	}
	for _, k := range keys {
		fmt.Fprintf(tw, "%s\t%d\n", k, stats[k])
	}
	return tw.Flush()
}

// RunCLI is the entry point called from cmd/cnc/main.go.
func RunCLI() {
	cli := NewCLI()
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}
