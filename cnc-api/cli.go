package cnc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
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
		Long: `CNC splits an input file across connected workers and runs your command on each part,
or broadcasts a command to all online workers simultaneously.

Examples:
  cnc spread targets.txt "nmap -iL {input}"
  cnc spread hosts.txt "python3 scan.py {input}" --workers 5 --watch
  cnc broadcast "hostname && whoami"
  cnc broadcast "uptime" --watch
  cnc workers
  cnc jobs
  cnc status <job-id>`,
	}

	root.PersistentFlags().StringVar(&c.serverURL, "server", c.serverURL, "CNC server HTTP address")

	root.AddCommand(
		c.cmdServerStart(),
		c.cmdWorkerStart(),
		c.cmdSpread(),
		c.cmdBroadcast(),
		c.cmdWorkers(),
		c.cmdJobs(),
		c.cmdStatus(),
		c.cmdServerTool(),
	)

	return root.Execute()
}

// ── server start ──────────────────────────────────────────────────────────────

func (c *CLI) cmdServerStart() *cobra.Command {
	return &cobra.Command{
		Use:   "server",
		Short: "Start the CNC server",
		RunE: func(cmd *cobra.Command, args []string) error {
			for {
				config, err := LoadServerConfig("server_config.json")
				if err != nil {
					config = DefaultServerConfig()
				}
				fmt.Printf("CNC Server\n  HTTP : %s\n  TCP  : %s\n  Data : %s\n\n",
					config.HTTPAddr, config.TCPAddr, config.DataDir)
				srv := NewServer(config)
				err = srv.Start()
				if srv.IsRestarting() || err == ErrRestartRequested {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				if err != nil && err != http.ErrServerClosed {
					return err
				}
				return nil
			}
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

// ── spread ────────────────────────────────────────────────────────────────────

func (c *CLI) cmdSpread() *cobra.Command {
	var (
		workers    int
		timeoutSec int
		watch      bool
	)

	cmd := &cobra.Command{
		Use:   "spread <file> <command>",
		Short: "Split a file across workers and run a command on each part",
		Long: `Split <file> into equal parts and run <command> on each part across connected workers.
Use {input} in your command as the placeholder for each chunk's local path on the worker.

Examples:
  cnc spread targets.txt "nmap -iL {input}"
  cnc spread hosts.txt "python3 scan.py {input}" --workers 5 --watch
  cnc spread data.txt "grep -c pattern {input}" --workers 3`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.submitJob(args[0], args[1], JobModeSpread, workers, timeoutSec, watch)
		},
	}

	cmd.Flags().IntVarP(&workers, "workers", "w", 0, "Number of parts to split into (default: all online workers)")
	cmd.Flags().IntVarP(&timeoutSec, "timeout", "t", DefaultTimeout, "Per-task timeout in seconds")
	cmd.Flags().BoolVar(&watch, "watch", false, "Stream progress until the job completes")

	return cmd
}

// ── broadcast ─────────────────────────────────────────────────────────────────

func (c *CLI) cmdBroadcast() *cobra.Command {
	var (
		timeoutSec int
		watch      bool
	)

	cmd := &cobra.Command{
		Use:   "broadcast <command>",
		Short: "Run a command on every online worker simultaneously",
		Long: `Run <command> on every currently-online worker at the same time.
No file splitting is performed — the command runs as-is on each worker.

Examples:
  cnc broadcast "hostname && whoami"
  cnc broadcast "uptime" --watch
  cnc broadcast "apt-get update -y" --timeout 120`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.submitJob("", args[0], JobModeBroadcast, 0, timeoutSec, watch)
		},
	}

	cmd.Flags().IntVarP(&timeoutSec, "timeout", "t", DefaultTimeout, "Per-task timeout in seconds")
	cmd.Flags().BoolVar(&watch, "watch", false, "Stream results until the job completes")

	return cmd
}

// submitJob is the shared job-submission logic for spread and broadcast.
func (c *CLI) submitJob(inputFile, command string, mode JobMode, workers, timeoutSec int, watch bool) error {
	if timeoutSec <= 0 {
		timeoutSec = DefaultTimeout
	}

	job := Job{
		Command:        command,
		Mode:           mode,
		Workers:        workers,
		TimeoutSeconds: timeoutSec,
	}
	if inputFile != "" {
		job.InputFile = inputFile
		// Use the filename as the default job name for spread jobs.
		parts := strings.Split(inputFile, "/")
		job.Name = parts[len(parts)-1]
	} else {
		// Derive a short name from the command for broadcast jobs.
		words := strings.Fields(command)
		if len(words) > 0 {
			job.Name = words[0]
		} else {
			job.Name = "broadcast"
		}
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
	fmt.Printf("  mode:     %s\n", mode)
	fmt.Printf("  command:  %s\n", command)
	if inputFile != "" {
		fmt.Printf("  file:     %s\n", inputFile)
	}
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
	if mode == JobModeBroadcast {
		return c.watchBroadcastJob(jobID)
	}
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

// watchBroadcastJob polls /api/jobs/{id}/tasks and prints per-worker results.
func (c *CLI) watchBroadcastJob(jobID string) error {
	// Track which tasks we've already printed a result for.
	printed := make(map[string]bool)

	for {
		time.Sleep(2 * time.Second)

		// Check job status first.
		jr, err := c.client.Get(c.serverURL + "/api/jobs/" + jobID)
		if err != nil {
			fmt.Printf("  poll error: %v\n", err)
			continue
		}
		var j Job
		if err := json.NewDecoder(jr.Body).Decode(&j); err != nil {
			jr.Body.Close()
			continue
		}
		jr.Body.Close()

		// Fetch tasks.
		tr, err := c.client.Get(c.serverURL + "/api/jobs/" + jobID + "/tasks")
		if err != nil {
			fmt.Printf("  poll error: %v\n", err)
			continue
		}
		var tasks []Task
		if err := json.NewDecoder(tr.Body).Decode(&tasks); err != nil {
			tr.Body.Close()
			continue
		}
		tr.Body.Close()

		for _, t := range tasks {
			if printed[t.ID] {
				continue
			}
			if t.Status != TaskStatusCompleted && t.Status != TaskStatusFailed {
				continue
			}
			printed[t.ID] = true

			workerID := t.AssignedTo
			if workerID == "" {
				workerID = "unknown"
			}

			if t.Status == TaskStatusCompleted && t.Result != nil && t.Result.ExitCode == 0 {
				output := strings.TrimSpace(t.Result.Stdout)
				if len(output) > 120 {
					output = output[:120] + "…"
				}
				fmt.Printf("[%s]  ✓  exit=0  %s\n", workerID, output)
			} else {
				exitCode := 1
				errOutput := t.Error
				if t.Result != nil {
					exitCode = t.Result.ExitCode
					if t.Result.Stderr != "" {
						errOutput = strings.TrimSpace(t.Result.Stderr)
					}
				}
				if len(errOutput) > 120 {
					errOutput = errOutput[:120] + "…"
				}
				fmt.Printf("[%s]  ✗  exit=%d  %s\n", workerID, exitCode, errOutput)
			}
		}

		if j.Status == "completed" || j.Status == "failed" || j.Status == "cancelled" {
			fmt.Printf("\nDone: %s — %d ok, %d failed\n", j.Status, j.Completed, j.Failed)
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
			fmt.Fprintln(tw, "ID\tNAME\tMODE\tSTATUS\tDONE\tFAILED\tCREATED")
			for _, j := range jobs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n",
					j.ID, j.Name, j.Mode, j.Status,
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
	fmt.Printf("Mode:     %s\n", job.Mode)
	fmt.Printf("Command:  %s\n", job.Command)
	if job.InputFile != "" {
		fmt.Printf("File:     %s\n", job.InputFile)
	}
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

// ── server-tool ───────────────────────────────────────────────────────────────

func (c *CLI) cmdServerTool() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "server-tool",
		Aliases: []string{"server-tools", "tool", "tools"},
		Short:   "Manage and execute CNC tools (server-side and distributed)",
		Long: `Execute specialized tools directly on the server host or distributed across workers.

Available tools:
  reverseip-thc     THC.org Reverse IP lookup (server-side, rate-limit paced)
  reverseip-domain  ReverseIPDomain.com lookup (server-side)
  dork2             Google Search parser (distributed spread mode, auto-synced to Drive)
  cms-scan          CMS technology detector (worker cluster)
  enum              Smart CMS and admin enumerator (worker cluster)
  wp-bruter         WordPress brute forcer (distributed spread mode, shared passlist/userlist)

Examples:
  cnc tool list
  cnc tool run dork2 -f queries.txt -o results.txt -p 2 -c us
  cnc tool run reverseip-thc -f targets.txt -o results.txt
  cnc tool run reverseip-thc -f targets.txt -w 3 --delay 300ms
  cnc tool run reverseip-domain -f targets.txt -o domains.txt --api-key "xyz"
  cnc tool run dork2 -f queries.txt --direct
  cnc tool run wp-bruter -f sites.txt -o cracked.txt --passlist /root/pass.txt --userlist /root/users.txt
`,
	}

	cmd.AddCommand(
		c.cmdServerToolList(),
		c.cmdServerToolRun(),
	)

	return cmd
}

func (c *CLI) cmdServerToolList() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available server-side tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Try fetching from server first
			var tools []ToolDefinition
			resp, err := c.client.Get(c.serverURL + "/api/tools")
			if err == nil && resp.StatusCode == http.StatusOK {
				defer resp.Body.Close()
				_ = json.NewDecoder(resp.Body).Decode(&tools)
			}

			// Fallback to local registry if server unavailable
			if len(tools) == 0 {
				for _, t := range ToolsRegistry {
					tools = append(tools, t)
				}
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSCOPE\tEXECUTABLE\tNAME\tDESCRIPTION")
			for _, t := range tools {
				scope := t.Scope
				if scope == "" {
					scope = "worker"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					t.ID, scope, t.Executable, t.Name, t.Description)
			}
			return tw.Flush()
		},
	}
}

func (c *CLI) cmdServerToolRun() *cobra.Command {
	var (
		inputFile  string
		outputFile string
		threads    int
		delay      string
		limit      int
		format     string
		apiKey     string
		timeout    string
		rate       int
		pages      int
		country    string
		lang       string
		site       string
		token      string
		extraArgs  string
		direct     bool
		watch      bool

		passList         string
		userList         string
		concurrency      int
		batch            int
		reqTimeout       int
		reqDelay         int
		loginConcurrency int
		enumUsers        bool
	)

	cmd := &cobra.Command{
		Use:   "run <tool-id>",
		Short: "Execute a tool (server-side or distributed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolID := args[0]
			tool, exists := ToolsRegistry[toolID]
			if !exists {
				return fmt.Errorf("tool '%s' not found. Run 'cnc tool list' to view available tools", toolID)
			}

			if inputFile == "" {
				return fmt.Errorf("input file is required (use -f or --file)")
			}

			options := make(map[string]interface{})
			if threads > 0 {
				options["threads"] = threads
			}
			if delay != "" {
				options["delay"] = delay
			}
			if limit > 0 {
				options["limit"] = limit
			}
			if format != "" {
				options["format"] = format
			}
			if apiKey != "" {
				options["api_key"] = apiKey
			}
			if timeout != "" {
				options["timeout"] = timeout
			}
			if rate > 0 {
				options["rate"] = rate
			}
			if pages > 0 {
				options["pages"] = pages
			}
			if country != "" {
				options["country"] = country
			}
			if lang != "" {
				options["lang"] = lang
			}
			if site != "" {
				options["site"] = site
			}
			if token != "" {
				options["token"] = token
			}
			if concurrency > 0 {
				options["concurrency"] = concurrency
			}
			if batch > 0 {
				options["batch"] = batch
			}
			if reqTimeout > 0 {
				options["req_timeout"] = reqTimeout
			}
			if reqDelay > 0 {
				options["req_delay"] = reqDelay
			}
			if loginConcurrency > 0 {
				options["login_concurrency"] = loginConcurrency
			}
			if !enumUsers {
				options["enum"] = false
			}
			if extraArgs != "" {
				options["extra_args"] = extraArgs
			}
			if outputFile != "" {
				options["output_file"] = outputFile
			}

			toolCmd, err := GenerateToolCommand(toolID, inputFile, options)
			if err != nil {
				return err
			}

			if direct {
				return c.executeDirectTool(tool.Executable, toolCmd)
			}

			// Submit via CNC Server API
			reqPayload := map[string]interface{}{
				"tool_id":     toolID,
				"input_file":  inputFile,
				"output_file": outputFile,
				"options":     options,
			}
			if passList != "" {
				reqPayload["passlist"] = passList
			}
			if userList != "" {
				reqPayload["userlist"] = userList
			}
			body, err := json.Marshal(reqPayload)
			if err != nil {
				return err
			}

			resp, err := c.client.Post(c.serverURL+"/api/tools/launch", "application/json", strings.NewReader(string(body)))
			if err != nil {
				fmt.Printf("Cannot reach CNC server at %s (%v)\nFalling back to direct local execution...\n\n", c.serverURL, err)
				return c.executeDirectTool(tool.Executable, toolCmd)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				var errResp map[string]string
				_ = json.NewDecoder(resp.Body).Decode(&errResp)
				return fmt.Errorf("server error (%d): %s", resp.StatusCode, errResp["error"])
			}

			var launchRes map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&launchRes); err != nil {
				return err
			}

			jobID := fmt.Sprintf("%v", launchRes["job_id"])
			fmt.Printf("Server tool job launched: %s\n", jobID)
			fmt.Printf("  tool:    %s\n", toolID)
			fmt.Printf("  command: %s\n", launchRes["command"])
			fmt.Printf("  mode:    %s\n", launchRes["mode"])
			if outputFile != "" {
				fmt.Printf("  output:  %s\n", outputFile)
			}

			if !watch {
				fmt.Printf("\nCheck progress: cnc status %s\n", jobID)
				return nil
			}

			fmt.Println()
			return c.watchJob(jobID)
		},
	}

	cmd.Flags().StringVarP(&inputFile, "file", "f", "", "Path to input file containing targets (required)")
	cmd.Flags().StringVarP(&outputFile, "out", "o", "", "Output filename for results")
	cmd.Flags().IntVarP(&threads, "threads", "t", 0, "Concurrency threads/workers (default from tool definition)")
	cmd.Flags().IntVarP(&threads, "workers", "w", 0, "Alias for --threads")
	cmd.Flags().StringVar(&delay, "delay", "", "Pacing delay between requests (e.g. 250ms, for reverseip-thc)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Max results per target IP (for reverseip-thc)")
	cmd.Flags().StringVar(&format, "format", "", "Output format: 'domains' or 'ip-domains'")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "API key (for reverseip-domain)")
	cmd.Flags().StringVar(&timeout, "timeout", "", "HTTP timeout (e.g. 30s)")
	cmd.Flags().IntVar(&rate, "rate", 0, "Rate limit in requests/sec")
	cmd.Flags().IntVarP(&pages, "pages", "p", 0, "Max search pages per query (for dork2)")
	cmd.Flags().StringVarP(&country, "country", "c", "", "Country code (e.g. us, uk, de, for dork2)")
	cmd.Flags().StringVarP(&lang, "lang", "l", "", "Language code (e.g. en, fr, for dork2)")
	cmd.Flags().StringVarP(&site, "site", "s", "", "Limit search to specific site (for dork2)")
	cmd.Flags().StringVar(&token, "token", "", "Apify API token (for dork2)")
	cmd.Flags().StringVar(&passList, "passlist", "", "Path to password list file (for wp-bruter, server-side)")
	cmd.Flags().StringVar(&userList, "userlist", "", "Path to username list file (for wp-bruter, server-side)")
	cmd.Flags().IntVar(&concurrency, "concurrency", 0, "Concurrent workers (for wp-bruter)")
	cmd.Flags().IntVar(&batch, "batch", 0, "Login attempts per multicall batch (for wp-bruter)")
	cmd.Flags().IntVar(&reqTimeout, "req-timeout", 0, "Request timeout in seconds (for wp-bruter)")
	cmd.Flags().IntVar(&reqDelay, "req-delay", 0, "Delay between batch requests in ms (for wp-bruter)")
	cmd.Flags().IntVar(&loginConcurrency, "login-concurrency", 0, "Concurrent password attempts per site (for wp-bruter)")
	cmd.Flags().BoolVar(&enumUsers, "enum", true, "Enumerate usernames via ?author=N and REST API (for wp-bruter)")
	cmd.Flags().StringVar(&extraArgs, "extra", "", "Extra arguments to pass directly to tool")
	cmd.Flags().BoolVar(&direct, "direct", false, "Execute directly as local process on server host, bypassing server daemon")
	cmd.Flags().BoolVar(&watch, "watch", true, "Watch progress until completed")

	return cmd
}

func (c *CLI) executeDirectTool(_ string, toolCmd string) error {
	fmt.Printf("[*] Running local server-side command: %s\n", toolCmd)
	cmd := exec.Command("sh", "-c", toolCmd)

	currPath := os.Getenv("PATH")
	exeDir, _ := os.Getwd()
	toolsDir := filepath.Join(exeDir, "tools")
	cmd.Env = append(os.Environ(), fmt.Sprintf("PATH=%s:%s:%s", toolsDir, exeDir, currPath))

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}
