package version

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

// ValidateResult reconstructs the observation from candidates instead of
// trusting a worker-supplied version, confidence, or match list.
func ValidateResult(record ResultRecordV1, artifact ArtifactV1, shardID string) error {
	if record.SchemaVersion != 1 || record.JobID != artifact.JobID || record.ShardID != shardID {
		return fmt.Errorf("result job or shard binding mismatch")
	}
	target, err := CanonicalTarget(record.Target.URL())
	if err != nil || target != record.Target {
		return fmt.Errorf("result target is not canonical")
	}
	if !artifact.TargetScope.Unrestricted() && !contains(artifact.TargetScope.Schemes, target.Scheme) {
		return fmt.Errorf("result scheme outside scope")
	}
	portOK := artifact.TargetScope.Unrestricted()
	for _, p := range artifact.TargetScope.Ports {
		portOK = portOK || p == target.Port
	}
	if !portOK {
		return fmt.Errorf("result port outside scope")
	}
	observation, err := ResolveEvidence(record.Observation.Component, record.Observation.EvidenceCandidates)
	if err != nil || !reflect.DeepEqual(observation, record.Observation) {
		return fmt.Errorf("result evidence is inconsistent")
	}
	matches := MatchRules(observation, artifact.Rules)
	if len(artifact.Rules) > 0 && len(matches) == 0 {
		return fmt.Errorf("strict result has no matching rules")
	}
	if len(matches) != len(record.MatchedRules) {
		return fmt.Errorf("result match list is inconsistent")
	}
	for i := range matches {
		if matches[i] != record.MatchedRules[i] {
			return fmt.Errorf("result match list is inconsistent")
		}
	}
	return nil
}

func DecodeResultLine(line []byte) (ResultRecordV1, error) {
	var record ResultRecordV1
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, fmt.Errorf("invalid result JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return record, fmt.Errorf("invalid trailing result content")
	}
	return record, nil
}

type ResultFile struct {
	mu                  sync.Mutex
	file                *os.File
	writer              *bufio.Writer
	directory, basename string
	closed              bool
	count               int
}

func NewResultFile(directory, jobID, shardID string) (*ResultFile, error) {
	name, err := ResultBasename(jobID, shardID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(directory, ".versions-*")
	if err != nil {
		return nil, err
	}
	return &ResultFile{file: file, writer: bufio.NewWriter(file), directory: directory, basename: name}, nil
}
func (f *ResultFile) Append(record ResultRecordV1) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fmt.Errorf("result file is closed")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = f.writer.Write(append(data, '\n')); err != nil {
		return err
	}
	f.count++
	return nil
}
func (f *ResultFile) Finish() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return "", fmt.Errorf("result file is closed")
	}
	f.closed = true
	defer os.Remove(f.file.Name())
	if err := f.writer.Flush(); err != nil {
		f.file.Close()
		return "", err
	}
	if err := f.file.Sync(); err != nil {
		f.file.Close()
		return "", err
	}
	if err := f.file.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(f.directory, f.basename)
	if err := os.Link(f.file.Name(), path); err != nil {
		return "", err
	}
	dir, err := os.Open(f.directory)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return "", err
	}
	return path, nil
}
func (f *ResultFile) Abort() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.file.Close()
		os.Remove(f.file.Name())
	}
}
