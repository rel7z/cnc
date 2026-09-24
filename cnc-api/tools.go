package cnc

import (
	"fmt"
	"strings"
)

// ToolsRegistry holds the definitions of all available tools.
var ToolsRegistry = map[string]ToolDefinition{
	"cms-scan": {
		ID:               "cms-scan",
		Name:             "CMS Scanner",
		Description:      "Detects CMS platform (WordPress, Joomla, Drupal…) for each target. Results per-CMS to watcher folder.",
		Executable:       "cms-scan",
		Scope:            "worker",
		DefaultMode:      "spread",
		InputFlag:        "-l",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/file/domains.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-t",
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
	"enum": {
		ID:               "enum",
		Name:             "Smart CMS & Deep Enumerator",
		Description:      "Auto-cascading pipeline: detects WordPress → plugin scan, Joomla → extension scan, Unknown → 3rd-party admin panel enum. Results piped to watcher folder.",
		Executable:       "cnc-enum",
		Scope:            "worker",
		InputFlag:        "-l",
		OutputFlag:       "-o",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/file/domains.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-t",
				Description: "Number of concurrent threads per scanning phase",
				Type:        "number",
				Default:     50,
			},
			{
				Name:        "timeout",
				Flag:        "-timeout",
				Description: "HTTP request timeout per probe (e.g. 10s, 15s)",
				Type:        "string",
				Default:     "10s",
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
	"reverseip-domain": {
		ID:               "reverseip-domain",
		Name:             "Reverse IP (ReverseIPDomain)",
		Description:      "Fast Reverse IP lookup using reverseipdomain.com API. Server-side execution.",
		Executable:       "reverseip-domain",
		Scope:            "server",
		InputFlag:        "-l",
		OutputFlag:       "-out",
		InputLabel:       "IP Address List (Server Path)",
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
	"reverseip-thc": {
		ID:               "reverseip-thc",
		Name:             "Reverse IP (THC.org)",
		Description:      "Public Reverse IP lookup using ip.thc.org with rate limit pacing and 429 retries. Server-side execution.",
		Executable:       "reverseip-thc",
		Scope:            "server",
		InputFlag:        "-f",
		OutputFlag:       "-o",
		InputLabel:       "IP Address List (Server Path)",
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
	"wp-bruter": {
		ID:               "wp-bruter",
		Name:             "WordPress Brute Forcer",
		Description:      "Brute forces WordPress credentials via XML-RPC multicall and wp-login. Load balances sites across worker servers with shared passlist/userlist.",
		Executable:       "wp-bruter",
		Scope:            "worker",
		DefaultMode:      JobModeSpread,
		InputFlag:        "-sites",
		OutputFlag:       "-output",
		InputLabel:       "WordPress Sites File (Server Path)",
		InputPlaceholder: "/root/file/sites.txt",
		SharedFiles: []SharedFileSpec{
			{
				Name:        "passlist",
				Label:       "Password List (Server Path)",
				Flag:        "-passlist",
				Description: "Server-side path to password list. Automatically uploaded and distributed to every worker before execution.",
				Required:    true,
			},
			{
				Name:        "userlist",
				Label:       "Username List (Server Path, optional)",
				Flag:        "-userlist",
				Description: "Server-side path to additional username list. Workers enumerate built-in usernames unless overridden here.",
				Required:    false,
			},
		},
		Options: []ToolOption{
			{
				Name:        "concurrency",
				Flag:        "-concurrency",
				Description: "Number of concurrent workers",
				Type:        "number",
				Default:     100,
			},
			{
				Name:        "batch",
				Flag:        "-batch",
				Description: "Login attempts per multicall batch",
				Type:        "number",
				Default:     100,
			},
			{
				Name:        "req_timeout",
				Flag:        "-timeout",
				Description: "Request timeout in seconds",
				Type:        "number",
				Default:     10,
			},
			{
				Name:        "req_delay",
				Flag:        "-delay",
				Description: "Delay between batch requests in milliseconds",
				Type:        "number",
				Default:     0,
			},
			{
				Name:        "login_concurrency",
				Flag:        "-login-concurrency",
				Description: "Concurrent password attempts per site (wp-login fallback)",
				Type:        "number",
				Default:     10,
			},
			{
				Name:        "enum",
				Flag:        "-enum",
				Description: "Enumerate usernames via ?author=N and REST API",
				Type:        "boolean",
				Default:     true,
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
		InputFlag:        "-l",
		InputLabel:       "Target Domains File (Worker Path)",
		InputPlaceholder: "/root/merged/wordpress.txt",
		Options: []ToolOption{
			{
				Name:        "threads",
				Flag:        "-t",
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

		if optDef.Flag != "" {
			cmdParts = append(cmdParts, optDef.Flag, fmt.Sprintf("%v", val))
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
