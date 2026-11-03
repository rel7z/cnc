package version

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var transportID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func ValidTransportID(id string) bool { return transportID.MatchString(id) }

func ArtifactBytes(artifact ArtifactV1) ([]byte, string, error) {
	if err := ValidateArtifact(&artifact); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		return nil, "", err
	}
	if len(data) > 8*1024*1024 {
		return nil, "", fmt.Errorf("artifact exceeds size limit")
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}

func ValidateArtifact(a *ArtifactV1) error {
	if a.SchemaVersion != ArtifactSchemaVersionV1 || !ValidTransportID(a.JobID) {
		return fmt.Errorf("invalid artifact schema or job ID")
	}
	scope, err := NormalizeScope(a.TargetScope)
	if err != nil {
		return err
	}
	rules, err := NormalizeRules(a.Rules)
	if err != nil {
		return err
	}
	a.TargetScope, a.Rules = scope, rules
	return nil
}

func LoadArtifact(path, expectedDigest, jobID string) (ArtifactV1, error) {
	var artifact ArtifactV1
	if !ValidTransportID(jobID) || len(expectedDigest) != 64 {
		return artifact, fmt.Errorf("invalid artifact binding")
	}
	f, err := os.Open(path)
	if err != nil {
		return artifact, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8*1024*1024+1))
	if err != nil || len(data) > 8*1024*1024 {
		return artifact, fmt.Errorf("artifact cannot be read within size limit")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedDigest {
		return artifact, fmt.Errorf("artifact digest mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return artifact, fmt.Errorf("invalid artifact JSON")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return artifact, fmt.Errorf("unexpected artifact content")
	}
	if artifact.JobID != jobID {
		return artifact, fmt.Errorf("artifact job mismatch")
	}
	if err := ValidateArtifact(&artifact); err != nil {
		return artifact, err
	}
	return artifact, nil
}

// WriteImmutable publishes complete bytes without permitting an existing path
// to be overwritten. Linking a synced temporary file is atomic on this volume.
func WriteImmutable(directory, basename string, data []byte) (string, error) {
	if basename == "" || filepath.Base(basename) != basename || basename == "." || basename == ".." {
		return "", fmt.Errorf("invalid basename")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(directory, ".complete-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(directory, basename)
	if err = os.Link(f.Name(), path); err != nil {
		return "", err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return "", err
	}
	return path, nil
}

func ResultBasename(jobID, shardID string) (string, error) {
	if !ValidTransportID(jobID) || !ValidTransportID(shardID) {
		return "", fmt.Errorf("invalid result job or shard ID")
	}
	return "enum-versions-" + jobID + "-" + shardID + ".jsonl", nil
}
