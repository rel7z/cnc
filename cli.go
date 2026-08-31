package cnc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type CLI struct {
	config    *CLIConfig
	serverURL string
	tcpAddr   string
	client    *http.Client
}

type CLIConfig struct {
	ServerHTTP string `json:"server_http"`
	ServerTCP  string `json:"server_tcp"`
	ConfigFile string `json:"config_file"`
}

func DefaultCLIConfig() *CLIConfig {
	return &CLIConfig{
		ServerHTTP: "http://localhost:8080",
		ServerTCP:  "localhost:9090",
		ConfigFile: "./cnc_config.json",
	}
}

func NewCLI() *CLI {
	return &CLI{
		config:    DefaultCLIConfig(),
		client:    &http.Client{Timeout: 30 * time.Second},
		serverURL: "http://localhost:8080",
		tcpAddr:   "localhost:9090",
	}
}

func (c *CLI) LoadConfig() error {
	data, err := os.ReadFile(c.config.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return c.SaveConfig()
		}
		return err
	}
	return json.Unmarshal(data, c.config)
}

func (c *CLI) SaveConfig() error {
	data, err := json.MarshalIndent(c.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.config.ConfigFile, data, 0644)
}

func (c *CLI) Execute() error {
	rootCmd := &cobra.Command{
		Use:   "cnc",
		Short: "CNC - Distributed task cluster controller",
		Long:  `Command & Control CLI for managing distributed worker clusters`,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return c.LoadConfig()
		},
	}

	rootCmd.AddCommand(
		c.serverCmd(),
		c.workerCmd(),
		c.jobCmd(),
		c.workerListCmd(),
		c.statusCmd(),
		c.splitCmd(),
		c.configCmd(),
		c.deployCmd(),
	)

	return rootCmd.Execute()
}

func (c *CLI) serverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Manage CNC server",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start CNC server",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.startServer()
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop CNC server",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.stopServer()
			},
		},
	)
	return cmd
}

func (c *CLI) workerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Manage workers",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start worker agent",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.startWorker()
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop worker (send shutdown)",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.shutdownWorker()
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all workers",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.listWorkers()
			},
		},
	)
	return cmd
}

func (c *CLI) jobCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "job",
		Short: "Manage jobs",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "submit",
			Short: "Submit a new job",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.submitJob()
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List all jobs",
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.listJobs()
			},
		},
		&cobra.Command{
			Use:   "status [job-id]",
			Short: "Get job status",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.jobStatus(args[0])
			},
		},
		&cobra.Command{
			Use:   "cancel [job-id]",
			Short: "Cancel a job",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.cancelJob(args[0])
			},
		},
		&cobra.Command{
			Use:   "logs [job-id]",
			Short: "Get job logs",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.jobLogs(args[0])
			},
		},
	)
	return cmd
}

func (c *CLI) workerListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "workers",
		Short: "List all workers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.listWorkers()
		},
	}
}

func (c *CLI) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show cluster status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.showStatus()
		},
	}
}

func (c *CLI) splitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "split [input-file] [output-dir] [split-size]",
		Short: "Split a large file into chunks",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.splitFile(args[0], args[1], args[2])
		},
	}
}

func (c *CLI) configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI configuration",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show current config",
			RunE: func(cmd *cobra.Command, args []string) error {
				data, _ := json.MarshalIndent(c.config, "", "  ")
				fmt.Println(string(data))
				return nil
			},
		},
		&cobra.Command{
			Use:   "set [key] [value]",
			Short: "Set config value",
			Args:  cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.setConfig(args[0], args[1])
			},
		},
	)
	return cmd
}

func (c *CLI) startServer() error {
	configPath := "./server_config.json"
	if _, err := os.Stat(configPath); err == nil {
		config, err := LoadServerConfig(configPath)
		if err != nil {
			return fmt.Errorf("load server config: %w", err)
		}
		server := NewServer(config)
		fmt.Printf("Starting CNC server on HTTP %s, TCP %s\n", config.HTTPAddr, config.TCPAddr)
		return server.Start()
	}
	server := NewServer(nil)
	fmt.Printf("Starting CNC server on HTTP %s, TCP %s\n", c.config.ServerHTTP, c.config.ServerTCP)
	return server.Start()
}

func (c *CLI) stopServer() error {
	fmt.Println("Server stop not implemented (send SIGTERM to server process)")
	return nil
}

func (c *CLI) startWorker() error {
	configPath := "./worker_config.json"
	if _, err := os.Stat(configPath); err == nil {
		config, err := LoadWorkerConfig(configPath)
		if err != nil {
			return fmt.Errorf("load worker config: %w", err)
		}
		worker := NewWorkerAgent(config)
		fmt.Printf("Starting worker %s, connecting to %s\n", config.WorkerID, config.ServerAddr)
		return worker.Start()
	}
	worker := NewWorkerAgent(&WorkerConfig{
		ServerAddr: c.config.ServerTCP,
		UseWebSocket: false,
	})
	fmt.Printf("Starting worker, connecting to %s\n", c.config.ServerTCP)
	return worker.Start()
}

func (c *CLI) shutdownWorker() error {
	conn, err := net.Dial("tcp", c.config.ServerTCP)
	if err != nil {
		return err
	}
	defer conn.Close()

	msg, _ := NewMessage(MsgTypeShutdownWorker, map[string]string{})
	encoder := json.NewEncoder(conn)
	return encoder.Encode(msg)
}

func (c *CLI) listWorkers() error {
	resp, err := c.client.Get(c.serverURL + "/api/workers")
	if err != nil {
		return c.listWorkersTCP()
	}
	defer resp.Body.Close()

	var workers []Worker
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return err
	}

	return c.printWorkers(workers)
}

func (c *CLI) listWorkersTCP() error {
	conn, err := net.Dial("tcp", c.config.ServerTCP)
	if err != nil {
		return err
	}
	defer conn.Close()

	msg, _ := NewMessage(MsgTypeGetWorkers, map[string]string{})
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		return err
	}

	decoder := json.NewDecoder(conn)
	var response map[string]interface{}
	if err := decoder.Decode(&response); err != nil {
		return err
	}

	fmt.Println("Workers (via TCP):")
	data, _ := json.MarshalIndent(response, "", "  ")
	fmt.Println(string(data))
	return nil
}

func (c *CLI) printWorkers(workers []Worker) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tADDRESS\tSTATUS\tLOAD\tMAX\tCAPABILITIES\tLAST SEEN")
	for _, worker := range workers {
		caps := strings.Join(worker.Capabilities, ",")
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			worker.ID, worker.Address, worker.Status,
			worker.CurrentLoad, worker.MaxTasks, caps,
			worker.LastSeen.Format("15:04:05"),
		)
	}
	return w.Flush()
}

func (c *CLI) submitJob() error {
	fmt.Println("Submit job interactively:")

	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Job name: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)

	fmt.Print("Type (domain_resolve/ip_scan): ")
	jobType, _ := reader.ReadString('\n')
	jobType = strings.TrimSpace(jobType)

	fmt.Print("Input file: ")
	inputFile, _ := reader.ReadString('\n')
	inputFile = strings.TrimSpace(inputFile)

	fmt.Print("Output directory: ")
	outputDir, _ := reader.ReadString('\n')
	outputDir = strings.TrimSpace(outputDir)

	fmt.Print("Split size (bytes, default 10MB): ")
	splitSizeStr, _ := reader.ReadString('\n')
	splitSizeStr = strings.TrimSpace(splitSizeStr)
	splitSize := int64(10 * 1024 * 1024)
	if splitSizeStr != "" {
		fmt.Sscanf(splitSizeStr, "%d", &splitSize)
	}

	job := Job{
		Name:      name,
		Type:      TaskType(jobType),
		InputFile: inputFile,
		OutputDir: outputDir,
		SplitSize: splitSize,
		Workers:   []string{},
	}

	resp, err := c.client.Post(c.serverURL+"/api/jobs", "application/json", strings.NewReader(mustMarshal(job)))
	if err != nil {
		return c.submitJobTCP(job)
	}
	defer resp.Body.Close()

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	fmt.Printf("Job submitted: %s\n", result["job_id"])
	return nil
}

func (c *CLI) submitJobTCP(job Job) error {
	conn, err := net.Dial("tcp", c.config.ServerTCP)
	if err != nil {
		return err
	}
	defer conn.Close()

	msg, _ := NewMessage(MsgTypeSubmitJob, SubmitJobPayload{Job: job})
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		return err
	}

	decoder := json.NewDecoder(conn)
	var result map[string]string
	if err := decoder.Decode(&result); err != nil {
		return err
	}
	fmt.Printf("Job submitted: %s\n", result["job_id"])
	return nil
}

func (c *CLI) listJobs() error {
	resp, err := c.client.Get(c.serverURL + "/api/jobs")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var jobs []Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tTYPE\tSTATUS\tTOTAL\tCOMPLETED\tFAILED\tCREATED")
	for _, job := range jobs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\n",
			job.ID, job.Name, job.Type, job.Status,
			job.TotalTasks, job.Completed, job.Failed,
			job.CreatedAt.Format("15:04:05"),
		)
	}
	return w.Flush()
}

func (c *CLI) jobStatus(jobID string) error {
	resp, err := c.client.Get(c.serverURL + "/api/jobs/" + jobID)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var job Job
	if err := json.NewDecoder(resp.Body).Decode(&job); err != nil {
		return err
	}

	data, _ := json.MarshalIndent(job, "", "  ")
	fmt.Println(string(data))
	return nil
}

func (c *CLI) cancelJob(jobID string) error {
	req, _ := http.NewRequest("DELETE", c.serverURL+"/api/jobs/"+jobID, nil)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	fmt.Println("Job cancelled")
	return nil
}

func (c *CLI) jobLogs(jobID string) error {
	fmt.Printf("Logs for job %s not implemented yet\n", jobID)
	return nil
}

func (c *CLI) showStatus() error {
	resp, err := c.client.Get(c.serverURL + "/api/stats")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var stats map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return err
	}

	data, _ := json.MarshalIndent(stats, "", "  ")
	fmt.Println(string(data))
	return nil
}

func (c *CLI) splitFile(inputFile, outputDir, splitSizeStr string) error {
	splitSize := int64(10 * 1024 * 1024)
	fmt.Sscanf(splitSizeStr, "%d", &splitSize)

	server := NewServer(nil)
	splitFiles, err := server.SplitInputFile(inputFile, splitSize, outputDir)
	if err != nil {
		return err
	}

	fmt.Printf("Split into %d files:\n", len(splitFiles))
	for _, f := range splitFiles {
		fmt.Println("  ", f)
	}
	return nil
}

func (c *CLI) setConfig(key, value string) error {
	switch key {
	case "server_http":
		c.config.ServerHTTP = value
		c.serverURL = value
	case "server_tcp":
		c.config.ServerTCP = value
		c.tcpAddr = value
	case "config_file":
		c.config.ConfigFile = value
	default:
		return fmt.Errorf("unknown config key: %s", key)
	}
	return c.SaveConfig()
}

func (c *CLI) doRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.serverURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.client.Do(req)
}

func mustMarshal(v interface{}) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (c *CLI) deployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build and deploy CNC to Linux servers",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "build [server|worker|all]",
			Short: "Build binary for Linux",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				target := "all"
				if len(args) > 0 {
					target = args[0]
				}
				return c.buildBinary(target)
			},
		},
		&cobra.Command{
			Use:   "install [server|worker]",
			Short: "Install systemd service on current machine",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return c.installService(args[0])
			},
		},
		&cobra.Command{
			Use:   "push <user@host> [server|worker]",
			Short: "Deploy binary and config to remote server via SSH",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				role := "server"
				if len(args) > 1 {
					role = args[1]
				}
				return c.pushDeploy(args[0], role)
			},
		},
		&cobra.Command{
			Use:   "gen-config [server|worker]",
			Short: "Generate example config files",
			Args:  cobra.MaximumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				role := "server"
				if len(args) > 0 {
					role = args[0]
				}
				return c.genConfig(role)
			},
		},
	)
	return cmd
}

func (c *CLI) buildBinary(target string) error {
	targets := []string{"server", "worker"}
	if target != "all" {
		targets = []string{target}
	}

	for _, t := range targets {
		output := fmt.Sprintf("cnc-%s", t)
		cmd := exec.Command("go", "build", "-o", output, "-ldflags", "-s -w", "./cmd/cnc/main.go")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Printf("Building %s...\n", output)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("build %s: %w", t, err)
		}
		fmt.Printf("  -> %s\n", output)
	}
	return nil
}

func (c *CLI) installService(role string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install requires root (run with sudo)")
	}

	binary := "cnc"
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("binary %s not found, run 'cnc deploy build' first", binary)
	}

	installDir := "/opt/cnc"
	os.MkdirAll(installDir, 0755)

	dst := filepath.Join(installDir, "cnc")
	if err := copyFile(binary, dst); err != nil {
		return err
	}
	os.Chmod(dst, 0755)

	var serviceContent string
	var configSrc, configDst string

	switch role {
	case "server":
		serviceContent = serverServiceTemplate
		configSrc = "./server_config.json"
		configDst = filepath.Join(installDir, "server_config.json")
	case "worker":
		serviceContent = workerServiceTemplate
		configSrc = "./worker_config.json"
		configDst = filepath.Join(installDir, "worker_config.json")
	default:
		return fmt.Errorf("role must be 'server' or 'worker'")
	}

	if _, err := os.Stat(configSrc); err == nil {
		copyFile(configSrc, configDst)
	}

	servicePath := fmt.Sprintf("/etc/systemd/system/cnc-%s.service", role)
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return err
	}

	exec.Command("systemctl", "daemon-reload").Run()
	exec.Command("systemctl", "enable", fmt.Sprintf("cnc-%s", role)).Run()
	fmt.Printf("Installed cnc-%s service. Start with: systemctl start cnc-%s\n", role, role)
	return nil
}

func (c *CLI) pushDeploy(host, role string) error {
	binary := fmt.Sprintf("cnc-%s", role)
	if _, err := os.Stat(binary); err != nil {
		return fmt.Errorf("binary %s not found, run 'cnc deploy build %s' first", binary, role)
	}

	configSrc := fmt.Sprintf("./%s_config.json", role)
	configDst := fmt.Sprintf("/opt/cnc/%s_config.json", role)

	cmds := []*exec.Cmd{
		exec.Command("ssh", host, "sudo mkdir -p /opt/cnc"),
		exec.Command("scp", binary, fmt.Sprintf("%s:/opt/cnc/cnc", host)),
		exec.Command("ssh", host, "sudo chmod 755 /opt/cnc/cnc"),
	}
	if _, err := os.Stat(configSrc); err == nil {
		cmds = append(cmds, exec.Command("scp", configSrc, fmt.Sprintf("%s:%s", host, configDst)))
	}

	for _, cmd := range cmds {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
	}

	serviceTmpl := serverServiceTemplate
	if role == "worker" {
		serviceTmpl = workerServiceTemplate
	}
	serviceCmd := exec.Command("ssh", host, fmt.Sprintf("cat | sudo tee /etc/systemd/system/cnc-%s.service", role))
	serviceCmd.Stdin = strings.NewReader(serviceTmpl)
	serviceCmd.Stdout = os.Stdout
	serviceCmd.Stderr = os.Stderr
	if err := serviceCmd.Run(); err != nil {
		return err
	}

	exec.Command("ssh", host, "sudo systemctl daemon-reload").Run()
	exec.Command("ssh", host, fmt.Sprintf("sudo systemctl enable --now cnc-%s", role)).Run()

	fmt.Printf("Deployed %s to %s\n", role, host)
	return nil
}

func (c *CLI) genConfig(role string) error {
	switch role {
	case "server":
		return os.WriteFile("server_config.json", []byte(serverConfigExample), 0644)
	case "worker":
		return os.WriteFile("worker_config.json", []byte(workerConfigExample), 0644)
	default:
		return fmt.Errorf("role must be 'server' or 'worker'")
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

const serverServiceTemplate = `[Unit]
Description=CNC Server
After=network.target

[Service]
Type=simple
User=cnc
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc -mode=server
Restart=on-failure
RestartSec=5
Environment=GIN_MODE=release
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`

const workerServiceTemplate = `[Unit]
Description=CNC Worker
After=network.target

[Service]
Type=simple
User=cnc
WorkingDirectory=/opt/cnc
ExecStart=/opt/cnc/cnc -mode=worker
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`

const serverConfigExample = `{
  "http_addr": "0.0.0.0:8080",
  "tcp_addr": "0.0.0.0:9090",
  "data_dir": "/opt/cnc/data",
  "max_retries": 3,
  "heartbeat_ttl": "30s"
}
`

const workerConfigExample = `{
  "server_addr": "SERVER_IP:9090",
  "worker_id": "worker-01",
  "max_tasks": 8,
  "capabilities": ["domain_resolve", "ip_scan"],
  "data_dir": "/opt/cnc/worker_data",
  "use_websocket": false
}
`

func RunCLI() {
	cli := NewCLI()
	if err := cli.Execute(); err != nil {
		log.Fatal(err)
	}
}