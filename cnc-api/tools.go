package cnc

import (
	"fmt"
	"strconv"
	"strings"
)

// ToolsRegistry holds the definitions of all available tools.
var ToolsRegistry = map[string]ToolDefinition{
	"cms-scan": {
		ID:               "cms-scan",
		Name:             "CMS Scanner",
		Description:      "Detects CMS platform (WordPress, Joomla, Drupal…) for each target. Results per-CMS to watcher folder. Choose CMS & PHP Version Scanner for version checks.",
		Executable:       "cms-scan",
		Scope:            "worker",
		DefaultMode:      "spread",
		InputFlag:        "-l",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/file/domains.txt",
		Options: []ToolOption{
			{Name: "auto_concurrency", Flag: "-auto-concurrency", Description: "Automatically tune concurrency", Type: "boolean", Default: true},
			{
				Name:        "threads",
				Flag:        "-t",
				Description: "Maximum concurrent requests (1–256)",
				Type:        "number",
				Default:     256,
			},
			{
				Name: "timeout", Flag: "-timeout", Description: "HTTP timeout in seconds (1–120)", Type: "number", Default: 10,
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"enum": {
		ID:               "enum",
		Name:             "CMS & PHP Version Scanner",
		Description:      "Scan CMS, plugins and disclosed PHP runtime versions using saved version checks. PHP works independently of CMS classification.",
		Executable:       "cnc-enum",
		Scope:            "worker",
		DefaultMode:      "spread",
		InputFlag:        "-l",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/file/domains.txt",
		SharedFiles: []SharedFileSpec{
			{
				Name:        "wp_plugins",
				Label:       "WordPress Plugins List",
				Flag:        "-wp-plugins",
				Description: "Custom WordPress plugin wordlist (one per line). Handled by server and distributed to workers.",
				Required:    false,
				Type:        "textarea",
			},
			{
				Name:        "joomla_exts",
				Label:       "Joomla Extensions List",
				Flag:        "-joomla-exts",
				Description: "Custom Joomla extension wordlist (one per line). Handled by server and distributed to workers.",
				Required:    false,
				Type:        "textarea",
			},
		},
		Options: []ToolOption{
			{Name: "auto_concurrency", Flag: "-auto-concurrency", Description: "Automatically tune concurrency", Type: "boolean", Default: true},
			{
				Name:        "threads",
				Flag:        "-t",
				Description: "Maximum concurrent requests (1–256)",
				Type:        "number",
				Default:     256,
			},
			{
				Name:        "timeout",
				Flag:        "-timeout",
				Description: "HTTP request timeout in seconds (1–120)",
				Type:        "number",
				Default:     10,
			},
		},
	},
	"reverseip-domain": {
		ID:               "reverseip-domain",
		Name:             "Reverse IP (ReverseIPDomain)",
		Description:      "Reverse IP lookup using reverseipdomain.com. Load balances input across worker servers.",
		Executable:       "reverseip-domain",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-l",
		OutputFlag:       "-out",
		InputLabel:       "Domain / IPv4 List (auto-resolve & dedup, server path)",
		InputPlaceholder: "/root/file/ips.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-c",
				Description: "Number of concurrent workers",
				Type:        "number",
				Default:     20,
			},
			{
				Name:        "api_key",
				Flag:        "-key",
				Description: "API key for reverseipdomain.com (optional)",
				Type:        "string",
				Default:     "",
			},
			{
				Name:        "timeout",
				Flag:        "-timeout",
				Description: "HTTP request timeout per probe (e.g. 30s)",
				Type:        "string",
				Default:     "30s",
			},
			{
				Name:        "rate",
				Flag:        "-rate",
				Description: "Rate limit (requests/sec, 0 = unlimited)",
				Type:        "number",
				Default:     0,
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"subnet-finder": {
		ID:               "subnet-finder",
		Name:             "Subnet Finder",
		Description:      "Resolves domains, fetches CIDRs from RIPE Stat, expands them, and ICMP pings all IPs. Spread mode.",
		Executable:       "subnet-finder",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-l",
		OutputFlag:       "-out",
		InputLabel:       "Domain List",
		InputPlaceholder: "domains.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-c",
				Description: "Number of concurrent workers",
				Type:        "number",
				Default:     100,
			},
			{
				Name:        "timeout",
				Flag:        "-timeout",
				Description: "ICMP Ping timeout per IP (e.g. 2s, 500ms)",
				Type:        "string",
				Default:     "2s",
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"reverseip-dnsrift": {
		ID:               "reverseip-dnsrift",
		Name:             "Reverse IP (DNSRift)",
		Description:      "DNSRift reverse IP lookup. Load balances across workers and streams unique domains to Drive.",
		Executable:       "reverseip-dnsrift",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-f",
		OutputFlag:       "-o",
		InputLabel:       "Domain / IPv4 List (auto-resolve & dedup, server path)",
		InputPlaceholder: "/root/file/ips.txt",
		Options: []ToolOption{
			{Name: "limit", Flag: "-limit", Description: "Maximum domains per IP (0 uses the provider default)", Type: "number", Default: 0},
			{Name: "threads", Flag: "-w", Description: "Concurrent requests per worker (1-32)", Type: "number", Default: 2},
			{Name: "delay", Flag: "-delay", Description: "Minimum interval between requests per worker", Type: "string", Default: "500ms"},
			{Name: "timeout", Flag: "-timeout", Description: "HTTP request timeout", Type: "string", Default: "20s"},
			{Name: "retries", Flag: "-retries", Description: "Bounded retries for rate limits and transient errors", Type: "number", Default: 2},
		},
	},
	"reverseip-thc": {
		ID:               "reverseip-thc",
		Name:             "Reverse IP (THC.org)",
		Description:      "Public Reverse IP lookup using ip.thc.org with rate limit pacing and 429 retries. Load balances across worker servers.",
		Executable:       "reverseip-thc",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-f",
		OutputFlag:       "-o",
		InputLabel:       "Domain / IPv4 List (auto-resolve & dedup, server path)",
		InputPlaceholder: "/root/file/ips.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-w",
				Description: "Number of concurrent workers (recommended 2-5 to avoid rate limits)",
				Type:        "number",
				Default:     3,
			},
			{
				Name:        "delay",
				Flag:        "-delay",
				Description: "Pacing delay between requests per worker (e.g. 250ms)",
				Type:        "string",
				Default:     "250ms",
			},
			{
				Name:        "limit",
				Flag:        "-l",
				Description: "Max domains to return per IP (1-100)",
				Type:        "number",
				Default:     100,
			},
			{
				Name:        "format",
				Flag:        "-format",
				Description: "Output format: 'domains' (one per line, deduplicated) or 'ip-domains'",
				Type:        "string",
				Default:     "domains",
			},
			{
				Name:        "retries",
				Flag:        "-retries",
				Description: "Max retries on HTTP 429 rate limit",
				Type:        "number",
				Default:     3,
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"dork2": {
		ID:               "dork2",
		Name:             "Google Search Parser (Dork2)",
		Description:      "Parses Google Search queries into unique domains via Apify. Load balances queries across worker servers with auto GDrive sync.",
		Executable:       "dork2",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-f",
		OutputFlag:       "-o",
		InputLabel:       "Search Queries File (Server Path)",
		InputPlaceholder: "/root/file/queries.txt",
		Options: []ToolOption{
			{
				Name:        "pages",
				Flag:        "-p",
				Description: "Max pages per search query (default: 5)",
				Type:        "number",
				Default:     5,
			},
			{
				Name:        "country",
				Flag:        "-c",
				Description: "Country code (us, uk, de, ca, etc.)",
				Type:        "string",
				Default:     "us",
			},
			{
				Name:        "lang",
				Flag:        "-l",
				Description: "Language code (en, de, fr, es, etc.)",
				Type:        "string",
				Default:     "en",
			},
			{
				Name:        "site",
				Flag:        "-s",
				Description: "Limit results to specific site domain",
				Type:        "string",
				Default:     "",
			},
			{
				Name:        "token",
				Flag:        "--token",
				Description: "Apify API token override (optional)",
				Type:        "string",
				Default:     "",
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"wp-plugins-detector": {
		ID:               "wp-plugins-detector",
		Name:             "WP Plugins Detector",
		Description:      "Detects installed WordPress plugins by probing standard plugin directories and tracking successful responses.",
		Executable:       "wp-plugins-detector",
		Scope:            "worker",
		DefaultMode:      "spread",
		InputFlag:        "-domains",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/merged/wordpress.txt",
		Options: []ToolOption{
			{
				Name:        "plugins",
				Flag:        "-plugins",
				Description: "Path to plugins list",
				Type:        "string",
				Default:     "./tools/plugins.txt",
			},
			{
				Name:        "threads",
				Flag:        "-c",
				Description: "Number of concurrent threads",
				Type:        "number",
				Default:     50,
			},
			{
				Name:        "extra_args",
				Flag:        "",
				Description: "Additional arguments to pass to the tool",
				Type:        "string",
				Default:     "",
			},
		},
	},
	"wp-discovery": {
		ID:               "wp-discovery",
		Name:             "WordPress Discovery (Aggressive)",
		Description:      "Aggressively discover WordPress sites worldwide using multiple methods: direct scanning, reverse DNS, CIDR ranges, CT logs. Max resources, high concurrency (2000+ threads).",
		Executable:       "wp-discovery",
		Scope:            "server",
		DefaultMode:      "broadcast",
		InputFlag:        "-input",
		OutputFlag:       "-out",
		InputLabel:       "Targets (Domains/IPs/CIDRs, server path)",
		InputPlaceholder: "/root/file/targets.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-t",
				Description: "Concurrent threads (aggressive: 1000-5000, default: 2000)",
				Type:        "number",
				Default:     2000,
			},
			{
				Name:        "timeout",
				Flag:        "-timeout",
				Description: "HTTP timeout in seconds (1–30)",
				Type:        "number",
				Default:     5,
			},
			{
				Name:        "ports",
				Flag:        "-ports",
				Description: "Ports to scan (comma-separated)",
				Type:        "string",
				Default:     "80,443,8080,8888,8081",
			},
			{
				Name:        "ct_logs",
				Flag:        "-ct",
				Description: "Enable Certificate Transparency log scanning",
				Type:        "boolean",
				Default:     true,
			},
			{
				Name:        "cidr_scan",
				Flag:        "-cidr",
				Description: "Enable aggressive CIDR/IP range scanning",
				Type:        "boolean",
				Default:     false,
			},
			{
				Name:        "dns_reverse",
				Flag:        "-dns-rev",
				Description: "Enable aggressive reverse DNS lookup",
				Type:        "boolean",
				Default:     true,
			},
			{
				Name:        "level",
				Flag:        "-level",
				Description: "Aggressiveness level: low/medium/high/max",
				Type:        "string",
				Default:     "max",
			},
		},
	},
}

// GenerateToolCommand constructs the command string for a tool based on the provided inputs.
func GenerateToolCommand(toolID string, inputFile string, options map[string]interface{}) (string, error) {
	tool, exists := ToolsRegistry[toolID]
	if !exists {
		return "", fmt.Errorf("tool '%s' not found", toolID)
	}

	var cmdParts []string
	executable := tool.Executable
	if !strings.Contains(executable, "/") {
		executable = "./tools/" + executable
	}
	cmdParts = append(cmdParts, executable)

	// Input flag handling
	inFlag := tool.InputFlag
	if inFlag == "" {
		inFlag = "-l"
	}
	if inputFile != "" {
		cmdParts = append(cmdParts, inFlag, inputFile)
	}

	// Output flag handling
	if tool.OutputFlag != "" {
		if outVal, ok := options["output_file"]; ok && outVal != nil && fmt.Sprintf("%v", outVal) != "" {
			cmdParts = append(cmdParts, tool.OutputFlag, fmt.Sprintf("%v", outVal))
		}
	}

	if toolID == "cms-scan" {
		if value, ok := options["threads"]; ok {
			n, e := strconv.Atoi(fmt.Sprint(value))
			if e != nil || n < 1 || n > 256 {
				return "", fmt.Errorf("threads must be 1–256")
			}
		}
		if value, ok := options["auto_concurrency"]; ok {
			if _, valid := optionBool(value); !valid {
				return "", fmt.Errorf("auto_concurrency must be boolean")
			}
		}
	}
	// Apply options
	for _, optDef := range tool.Options {
		val, ok := options[optDef.Name]
		if !ok {
			val = optDef.Default
		}

		if val == nil || val == "" {
			continue
		}

		if optDef.Type == "boolean" {
			if b, valid := optionBool(val); valid {
				if b {
					cmdParts = append(cmdParts, optDef.Flag)
				} else {
					cmdParts = append(cmdParts, optDef.Flag+"=false")
				}
			}
			continue
		}

		if toolID == "reverseip-dnsrift" && optDef.Name == "limit" {
			limit, err := strconv.Atoi(fmt.Sprint(val))
			if err != nil || limit < 0 || limit > 100000 {
				return "", fmt.Errorf("DNSRift limit must be between 0 and 100000")
			}
			if limit == 0 {
				continue
			}
		}
		if optDef.Flag != "" {
			if toolID == "cms-scan" && optDef.Name == "timeout" {
				n, err := strconv.Atoi(fmt.Sprint(val))
				if err != nil || n < 1 || n > 120 {
					return "", fmt.Errorf("timeout must be 1–120 seconds")
				}
				cmdParts = append(cmdParts, optDef.Flag, strconv.Itoa(n)+"s")
			} else {
				cmdParts = append(cmdParts, optDef.Flag, fmt.Sprintf("%v", val))
			}
		} else {
			// No flag (e.g. extra_args), just append the value
			valStr := fmt.Sprintf("%v", val)
			if valStr != "" {
				cmdParts = append(cmdParts, valStr)
			}
		}
	}

	return strings.Join(cmdParts, " "), nil
}

// optionBool converts a raw option value into a boolean.
// It accepts Go bools, JSON bools, and common string representations.
func optionBool(val interface{}) (bool, bool) {
	switch v := val.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on", "enabled":
			return true, true
		case "false", "0", "no", "off", "disabled":
			return false, true
		}
	}
	return false, false
}
