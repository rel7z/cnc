package cnc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cnc-enum/version"
)

func TestEnumPublicationStreamsBeyondOldTransferLimit(t *testing.T) {
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: "large", TargetScope: version.TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{80}}}
	targets := map[version.TargetLocator]bool{}
	var readers []io.Reader
	hash := sha256.New()
	padding := strings.Repeat(" ", 1024*1024)
	for i := 0; i < 65; i++ {
		target, _ := version.CanonicalTarget(fmt.Sprintf("http://127.0.0.1/%d", i))
		targets[target] = true
		record := version.ResultRecordV1{SchemaVersion: 1, JobID: "large", ShardID: "shard", Target: target, MatchedRules: []version.MatchedRule{}, Observation: version.VersionObservation{Component: "wordpress:core", Confidence: "unknown", EvidenceCandidates: []version.EvidenceCandidate{}}}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, bytes.NewReader(data), strings.NewReader(padding), strings.NewReader("\n"))
		hash.Write(data)
		io.WriteString(hash, padding+"\n")
	}
	digest, err := stageEnumJSONL(io.MultiReader(readers...), io.Discard, artifact, "shard", targets)
	if err != nil || digest != hex.EncodeToString(hash.Sum(nil)) {
		t.Fatalf("digest=%s error=%v", digest, err)
	}
}

func TestEnumPublicationRejectsIncompleteDuplicateAndForeignRecords(t *testing.T) {
	target, _ := version.CanonicalTarget("http://127.0.0.1/")
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: "job", TargetScope: version.TargetScope{CIDRs: []string{"127.0.0.0/8"}, Schemes: []string{"http"}, Ports: []int{80}}}
	record := version.ResultRecordV1{SchemaVersion: 1, JobID: "job", ShardID: "shard", Target: target, MatchedRules: []version.MatchedRule{}, Observation: version.VersionObservation{Component: "wordpress:core", Confidence: "unknown", EvidenceCandidates: []version.EvidenceCandidate{}}}
	data, _ := json.Marshal(record)
	for _, input := range []string{string(data), string(data) + "\n" + string(data) + "\n", "{}\n", strings.Repeat("x", enumResultLineLimit) + "\n"} {
		if _, err := stageEnumJSONL(strings.NewReader(input), io.Discard, artifact, "shard", map[version.TargetLocator]bool{target: true}); err == nil {
			t.Fatal("invalid transfer accepted")
		}
	}
	if _, err := stageEnumJSONL(bytes.NewReader(append(data, '\n')), io.Discard, artifact, "shard", nil); err == nil {
		t.Fatal("foreign input accepted")
	}
}

func TestEnumImmutableFileCopyRetainsOriginalAndStreamsDigest(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "staged")
	if err := os.WriteFile(source, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	destination, err := copyEnumImmutable(filepath.Join(directory, "merged"), "result.jsonl", source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := copyEnumImmutable(filepath.Dir(destination), "result.jsonl", source); !os.IsExist(err) {
		t.Fatalf("overwrite error=%v", err)
	}
	expected := sha256.Sum256([]byte("first\n"))
	if digest, err := enumFileDigest(destination); err != nil || digest != hex.EncodeToString(expected[:]) {
		t.Fatalf("original changed: %s %v", digest, err)
	}
}

func TestEnumAcceptsCompletedPartialPublicationAfterCancellation(t *testing.T) {
	directory, merged := t.TempDir(), t.TempDir()
	input := filepath.Join(directory, "targets.txt")
	if err := os.WriteFile(input, []byte("http://127.0.0.1/\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(&ServerConfig{DataDir: directory, GDrive: &GDriveWrapper{WatchDir: merged}})
	jobID := launchFixtureEnum(t, s, input, 80)
	task := waitEnumTask(t, s, jobID)
	s.mu.Lock()
	task.AssignedTo = "cancelled-worker"
	task.Status = TaskStatusCancelled
	s.jobs[jobID].Status = "cancelled"
	token := s.jobs[jobID].Enum.Token
	if err := s.persistEnumJobLocked(jobID); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	observation, err := version.ResolveEvidence("wordpress:core", []version.EvidenceCandidate{{Source: "wp_generator", Rank: 20, Version: "6.4.0", RetrievedAt: time.Now().UTC(), Validation: version.EvidenceValidation{Kind: "wp_generator", ContentType: "text/html", Bytes: 80}}})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := version.CanonicalTarget("http://127.0.0.1/")
	record := version.ResultRecordV1{SchemaVersion: 1, JobID: jobID, ShardID: task.ID, Target: target, MatchedRules: []version.MatchedRule{{ID: "core", Name: "Owned core fixture"}}, Observation: observation}
	data, _ := json.Marshal(record)
	data = append(data, '\n')
	sum := sha256.Sum256(data)
	basename, _ := version.ResultBasename(jobID, task.ID)
	request := httptest.NewRequest(http.MethodPost, "/api/enum/results/"+task.ID, bytes.NewReader(data))
	request.Header.Set("X-Worker-ID", "cancelled-worker")
	request.Header.Set("X-Enum-Job-Token", token)
	request.Header.Set("X-Enum-Filename", basename)
	request.Header.Set("X-Enum-SHA256", hex.EncodeToString(sum[:]))
	response := httptest.NewRecorder()
	s.handleEnumAPI(response, request)
	if response.Code != 200 || task.EnumPublication != "accepted" || task.Status != TaskStatusCancelled || s.jobs[jobID].Status != "cancelled" {
		t.Fatalf("status=%d body=%s task=%+v", response.Code, response.Body, task)
	}
	if _, err := os.Stat(filepath.Join(merged, basename)); !os.IsNotExist(err) {
		t.Fatal("structured evidence exported to Drive watch directory")
	}
	stored, err := os.ReadFile(filepath.Join(directory, "enum-jobs", jobID, "publications", basename))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("completed partial output was not retained", err)
	}
}

func TestEnumPublicationAcceptsWordPressCoreFeedInventory(t *testing.T) {
	rules, err := version.NormalizeRules([]version.VersionRule{{ID: "core", Name: "WordPress core, any detected version", Component: "wordpress:core", Clauses: []version.VersionClause{{Operator: ">=", Version: "0.0.0-0"}}}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := version.ResolveEvidence("wordpress:core", []version.EvidenceCandidate{{Source: "wp_feed_generator", Rank: 25, Version: "6.8.1", RetrievedAt: time.Now().UTC(), Validation: version.EvidenceValidation{Kind: "wp_feed", ContentType: "application/rss+xml", Bytes: 120}}})
	if err != nil {
		t.Fatal(err)
	}
	target, _ := version.CanonicalTarget("http://127.0.0.1/")
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: "core-feed", Rules: rules}
	record := version.ResultRecordV1{SchemaVersion: 1, JobID: artifact.JobID, ShardID: "shard", Target: target, MatchedRules: version.MatchRules(observation, rules), Observation: observation}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.MatchedRules) != 1 {
		t.Fatal("inventory did not match")
	}
	var stored bytes.Buffer
	if _, err := stageEnumJSONL(bytes.NewReader(append(data, '\n')), &stored, artifact, "shard", map[version.TargetLocator]bool{target: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.Bytes(), append(data, '\n')) {
		t.Fatal("receipt changed")
	}
}

func TestEnumPublicationAcceptsReportedPHPVersion(t *testing.T) {
	rules, err := version.NormalizeRules([]version.VersionRule{{ID: "php", Name: "PHP 8.5 range", Component: "php:php-src", Clauses: []version.VersionClause{{Operator: ">=", Version: "8.5.0"}, {Operator: "<", Version: "8.5.12"}}}})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := version.ResolveEvidence("php:php-src", []version.EvidenceCandidate{{Source: "php_powered_by", Rank: 10, Version: "8.5.11", RetrievedAt: time.Now().UTC(), Validation: version.EvidenceValidation{Kind: "php_header", ContentType: "text/html", Bytes: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Confidence != version.ConfidenceReported {
		t.Fatal("runtime banner treated as authoritative")
	}
	target, _ := version.CanonicalTarget("http://127.0.0.1/")
	artifact := version.ArtifactV1{SchemaVersion: 1, JobID: "php-runtime", Rules: rules}
	record := version.ResultRecordV1{SchemaVersion: 1, JobID: artifact.JobID, ShardID: "shard", Target: target, MatchedRules: version.MatchRules(observation, rules), Observation: observation}
	data, _ := json.Marshal(record)
	if _, err := stageEnumJSONL(bytes.NewReader(append(data, '\n')), io.Discard, artifact, "shard", map[version.TargetLocator]bool{target: true}); err != nil {
		t.Fatal(err)
	}
	record.Observation.Confidence = version.ConfidenceAuthoritative
	data, _ = json.Marshal(record)
	if _, err := stageEnumJSONL(bytes.NewReader(append(data, '\n')), io.Discard, artifact, "shard", map[version.TargetLocator]bool{target: true}); err == nil {
		t.Fatal("inflated confidence accepted")
	}
}
