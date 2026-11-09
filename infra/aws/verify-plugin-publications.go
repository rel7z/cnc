// Run from the deployed enum module. Reads receipts and text exports only.
package main

import (
	"bufio"
	"cnc-enum/detection"
	"cnc-enum/version"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func must(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func cleanURL(raw string) string {
	u, err := url.Parse(raw)
	must(err == nil, "invalid result URL")
	if u.Scheme == "http" && u.Port() == "80" || u.Scheme == "https" && u.Port() == "443" {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	return u.String()
}
func main() {
	directory, watch := os.Args[1], os.Args[2]
	var state struct {
		ArtifactFilename string `json:"artifact_filename"`
		Job              struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Enum   struct {
				SHA string `json:"artifact_sha256"`
			} `json:"enum"`
		} `json:"job"`
		Tasks []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			Publication string `json:"enum_publication"`
			Result      *struct {
				Exit int `json:"exit_code"`
			} `json:"result"`
		} `json:"tasks"`
	}
	data, err := os.ReadFile(filepath.Join(directory, "state.json"))
	must(err == nil, "missing job state")
	must(json.Unmarshal(data, &state) == nil, "invalid job state")
	must(state.Job.Status == "completed" && len(state.Tasks) == 11, "scan not fully completed")
	artifact, err := version.LoadArtifact(filepath.Join(directory, state.ArtifactFilename), state.Job.Enum.SHA, state.Job.ID)
	must(err == nil, "invalid bound artifact")
	expected := map[string]map[string]bool{}
	references := map[string]map[string]version.ResultRecordV1{}
	add := func(name, line string, record version.ResultRecordV1) {
		if expected[name] == nil {
			expected[name] = map[string]bool{}
			references[name] = map[string]version.ResultRecordV1{}
		}
		expected[name][line] = true
		references[name][line] = record
	}
	records := 0
	for _, task := range state.Tasks {
		must(task.Status == "completed" && task.Publication == "accepted" && task.Result != nil && task.Result.Exit == 0, "task or publication not successful")
		name, err := version.ResultBasename(state.Job.ID, task.ID)
		must(err == nil, "invalid shard binding")
		f, err := os.Open(filepath.Join(directory, "publications", name))
		must(err == nil, "missing shard publication")
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			record, err := version.DecodeResultLine(scanner.Bytes())
			must(err == nil, "invalid result encoding")
			must(version.ValidateResult(record, artifact, task.ID) == nil, "record does not match artifact rules")
			records++
			component := strings.Split(record.Observation.Component, ":")
			if component[1] == "core" {
				continue
			}
			line := cleanURL(record.Target.URL())
			if component[0] == "wordpress" {
				add(component[2]+".txt", line, record)
				add("plugins_name.txt", strings.TrimRight(line, "/")+"/wp-content/plugins/"+component[2], record)
			} else if component[1] == "plugin" {
				add("plugin:"+component[2]+":"+component[3]+".txt", line, record)
			} else {
				prefix := map[string]string{"component": "com_", "module": "mod_", "library": "lib_", "template": "tpl_", "package": "pkg_"}[component[1]]
				add(prefix+component[2]+".txt", line, record)
			}
		}
		must(scanner.Err() == nil, "incomplete publication read")
		f.Close()
	}
	uniqueLines, rawLines := 0, 0
	guard, err := version.NewScopeGuard(artifact.TargetScope)
	must(err == nil, "invalid redirect validation scope")
	defer guard.CloseIdleConnections()
	client := guard.Client(5 * time.Second)
	canonical := map[version.TargetLocator]string{}
	aliases := 0
	for name, want := range expected {
		data, err := os.ReadFile(filepath.Join(watch, name))
		must(err == nil, "missing text export "+name)
		seen := map[string]bool{}
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			u, e := url.Parse(line)
			must(e == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && !strings.ContainsAny(line, " \t\r"), "unclean text line in "+name)
			seen[line] = true
			rawLines++
		}
		credited := map[string]bool{}
		for line := range want {
			if seen[line] {
				credited[line] = true
				continue
			}
			record := references[name][line]
			working, ok := canonical[record.Target]
			if !ok {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				r := detection.Classify(ctx, client, record.Target.URL())
				cancel()
				must(r.Err == nil && r.CMS == strings.Split(record.Observation.Component, ":")[0] && r.URL != "", "cannot verify redirect alias in "+name)
				working = cleanURL(r.URL)
				canonical[record.Target] = working
			}
			if name == "plugins_name.txt" {
				working = strings.TrimRight(working, "/") + "/wp-content/plugins/" + strings.Split(record.Observation.Component, ":")[2]
			}
			must(seen[working], "missing verified canonical export in "+name)
			credited[working] = true
			aliases++
		}
		must(len(credited) == len(seen), "unverified text lines in "+name)
		uniqueLines += len(seen)
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"job_id": state.Job.ID, "validated_shards": len(state.Tasks), "validated_records": records, "text_files": len(expected), "unique_text_lines": uniqueLines, "raw_text_lines": rawLines, "resolved_redirect_aliases": aliases, "all_text_lines_backed_by_matching_rule_records": true})
}
