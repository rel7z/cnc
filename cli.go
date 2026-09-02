package cnc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
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
		Short: "CNC — distributed shell task cluster",
		Long:  "Command & Control: submit shell jobs to a distributed worker cluster.",
	}

	// Global flag to override the server URL.
	root.PersistentFlags().StringVar(&c.serverURL, "server", c.serverURL, "CNC server HTTP address")

	root.AddCommand(
		c.cmdServer(),
		c.cmdWorker(),
		c.cmdJob(),
		c.cmdStatus(),
	)

	return root.Execute()
}

// ── server ────────────────────────────────────────────────────────────────────

func (c *CLI) cmdServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage the CNC server",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the CNC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.serverStart()
		},
	})
	return cmd
}

func (c *CLI) serverStart() error {
	config, err := LoadServerConfig("server_config.json")
	if err != nil {
		config = DefaultServerConfig()
	}
	fmt.Printf("Starting CNC server — HTTP %s  TCP %s\n", config.HTTPAddr, config.TCPAddr)
	srv := NewServer(config)
	return srv.Start()
}

// ── worker ────────────────────────────────────────────────────────────────────

func (c *CLI) cmdWorker() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage workers",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start a worker agent",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.workerStart()
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List connected workers",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.workerList()
			},
		},
	)
	return cmd
}

func (c *CLI) workerStart() error {
	config, err := LoadWorkerConfig("worker_config.json")
	if err != nil {
		config = DefaultWorkerConfig()
	}
	fmt.Printf("Starting worker %s — server %s  max_tasks=%d\n",
		config.WorkerID, config.ServerAddr, config.MaxTasks)
	w := NewWorkerAgent(config)
	return w.Start()
}

func (c *CLI) workerList() error {
	resp, err := c.client.Get(c.serverURL + "/api/workers")
	if err != nil {
		return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	var workers []Worker
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tLOAD/MAX\tLAST SEEN")
	for _, wk := range workers {
		fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%s\n",
			wk.ID, wk.Status,
			wk.CurrentLoad, wk.MaxTasks,
			wk.LastSeen.Format("15:04:05"),
		)
	}
	return tw.Flush()
}

// ── job ───────────────────────────────────────────────────────────────────────

func (c *CLI) cmdJob() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage jobs",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "submit",
			Short: "Submit a new job interactively",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.jobSubmit()
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all jobs",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.jobList()
			},
		},
		&cobra.Command{
			Use:   "status <job-id>",
			Short: "Get status of a specific job",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.jobStatus(args[0])
			},
		},
	)
	return cmd
}

func (c *CLI) jobSubmit() error {
	r := bufio.NewReader(os.Stdin)
	ask := func(prompt, fallback string) string {
		if fallback != "" {
			fmt.Printf("%s [%s]: ", prompt, fallback)
		} else {
			fmt.Printf("%s: ", prompt)
		}
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return fallback
		}
		return line
	}

	name := ask("Job name", "")
	if name == "" {
		return fmt.Errorf("job name is required")
	}

	command := ask("Command (use {input} and {output} for file mode, or just the command for pipe mode)", "")
	if command == "" {
		return fmt.Errorf("command is required")
	}

	execModeStr := ask("Exec mode (file/pipe)", "file")
	if execModeStr != "file" && execModeStr != "pipe" {
		return fmt.Errorf("exec mode must be 'file' or 'pipe'")
	}

	inputFile := ask("Input file path", "")
	if inputFile == "" {
		return fmt.Errorf("input file is required")
	}

	outputDir := ask("Output directory", "./output")

	var timeoutSec int
	timeoutStr := ask("Timeout per task in seconds", "300")
	fmt.Sscanf(timeoutStr, "%d", &timeoutSec)
	if timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}

	var splitSize int64
	splitStr := ask("Split size in bytes", "10485760")
	fmt.Sscanf(splitStr, "%d", &splitSize)
	if splitSize <= 0 {
		splitSize = DefaultSplitSize
	}

	job := Job{
		Name:           name,
		Command:        command,
		ExecMode:       ExecMode(execModeStr),
		InputFile:      inputFile,
		OutputDir:      outputDir,
		TimeoutSeconds: timeoutSec,
		SplitSize:      splitSize,
	}

	body, _ := json.Marshal(job)
	resp, err := c.client.Post(c.serverURL+"/api/jobs", "application/json",
		strings.NewReader(string(body)))
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
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	fmt.Printf("Job submitted: %s\n", result["job_id"])
	return nil
}

func (c *CLI) jobList() error {
	resp, err := c.client.Get(c.serverURL + "/api/jobs")
	if err != nil {
		return fmt.Errorf("cannot reach server at %s: %w", c.serverURL, err)
	}
	defer resp.Body.Close()

	var jobs []Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tMODE\tSTATUS\tTOTAL\tDONE\tFAILED\tCREATED")
	for _, j := range jobs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			j.ID, j.Name, j.ExecMode, j.Status,
			j.TotalTasks, j.Completed, j.Failed,
			j.CreatedAt.Format("15:04:05"),
		)
	}
	return tw.Flush()
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

	data, _ := json.MarshalIndent(job, "", "  ")
	fmt.Println(string(data))
	return nil
}

// ── status ────────────────────────────────────────────────────────────────────

func (c *CLI) cmdStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.clusterStatus()
		},
	}
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
		"workers_total", "workers_online",
		"jobs_total", "jobs_running",
		"tasks_total", "tasks_pending", "tasks_running", "tasks_completed", "tasks_failed",
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
