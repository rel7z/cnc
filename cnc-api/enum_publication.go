package cnc

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"cnc-enum/version"
)

// An artifact is bounded at 4 MiB. A record can contain its matching rule
// identities plus a bounded evidence plan; 8 MiB leaves room for both without
// imposing a limit on the complete shard's size.
const enumResultLineLimit = 8 * 1024 * 1024

func stageEnumJSONL(input io.Reader, destination io.Writer, artifact version.ArtifactV1, shardID string, targets map[version.TargetLocator]bool) (string, error) {
	hash := sha256.New()
	output := io.MultiWriter(destination, hash)
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), enumResultLineLimit)
	scanner.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return i + 1, data[:i+1], nil
		}
		if atEOF && len(data) != 0 {
			return 0, nil, fmt.Errorf("incomplete JSONL line")
		}
		return 0, nil, nil
	})
	seen := map[string]bool{}
	for scanner.Scan() {
		line := scanner.Bytes()
		record, err := version.DecodeResultLine(line)
		if err != nil || !targets[record.Target] || version.ValidateResult(record, artifact, shardID) != nil {
			return "", fmt.Errorf("invalid result record")
		}
		key := record.Target.URL() + "\n" + record.Observation.Component
		if seen[key] {
			return "", fmt.Errorf("duplicate target/component result")
		}
		seen[key] = true
		if _, err := output.Write(line); err != nil {
			return "", err
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func enumFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Copy to a temporary file on the destination volume, sync, then link without
// replacing an existing publication. This also works across mounted volumes.
func copyEnumImmutable(directory, basename, sourcePath string) (string, error) {
	if filepath.Base(basename) != basename || basename == "." || basename == ".." || basename == "" {
		return "", fmt.Errorf("invalid basename")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return "", err
	}
	defer source.Close()
	temporary, err := os.CreateTemp(directory, ".complete-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if _, err := io.Copy(temporary, source); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(directory, basename)
	if err := os.Link(temporary.Name(), path); err != nil {
		return "", err
	}
	dir, err := os.Open(directory)
	if err != nil {
		return "", err
	}
	defer dir.Close()
	return path, dir.Sync()
}
