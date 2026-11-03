package cnc

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var immutableEnumFilename = regexp.MustCompile(`^enum-versions-[a-z0-9][a-z0-9_-]{0,63}-[a-z0-9][a-z0-9_-]{0,63}\.jsonl$`)

func isImmutableEnumResult(filename string) bool { return immutableEnumFilename.MatchString(filename) }

// Enum publications are complete immutable bytes, not appendable text streams.
// Never deduplicate, normalize, or merge their JSONL records. Streaming avoids
// both bufio.Scanner's default line limit and loading a whole shard into RAM.
// Called while authMu is held by syncFile.
func (u *GDriveUploader) syncImmutableEnumResult(path, filename string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open enum result: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash enum result: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	remoteFiles, err := u.findDriveFilesByName(filename)
	if err != nil {
		return fmt.Errorf("find enum results in Drive: %w", err)
	}
	if len(remoteFiles) == 0 {
		metadata := &drive.File{Name: filename, MimeType: "application/x-ndjson", Description: "SHA256: " + digest}
		if u.config.ParentFolderID != "" {
			metadata.Parents = []string{u.config.ParentFolderID}
		}
		if _, err := u.service.Files.Create(metadata).SupportsAllDrives(true).
			Media(file, googleapi.ChunkSize(4*1024*1024)).Context(u.ctx).Do(); err != nil {
			return fmt.Errorf("create immutable enum result: %w", err)
		}
	} else {
		// Verify every same-named object before removing an identical duplicate.
		// Different remote content is a collision, never data to merge or replace.
		for _, remote := range remoteFiles {
			response, err := u.service.Files.Get(remote.Id).SupportsAllDrives(true).Context(u.ctx).Download()
			if err != nil {
				return fmt.Errorf("read immutable enum result: %w", err)
			}
			remoteHash := sha256.New()
			_, readErr := io.Copy(remoteHash, response.Body)
			response.Body.Close()
			if readErr != nil {
				return fmt.Errorf("hash remote enum result: %w", readErr)
			}
			if hex.EncodeToString(remoteHash.Sum(nil)) != digest {
				return fmt.Errorf("immutable enum result filename collision in Drive")
			}
		}
		for _, duplicate := range remoteFiles[1:] {
			if err := u.service.Files.Delete(duplicate.Id).SupportsAllDrives(true).Context(u.ctx).Do(); err != nil {
				return fmt.Errorf("remove identical enum result duplicate: %w", err)
			}
		}
	}
	u.uploadedMu.Lock()
	u.uploaded[filename+":"+digest] = true
	u.uploadedCount++
	u.lastUpload = time.Now()
	u.uploadedMu.Unlock()
	return nil
}
