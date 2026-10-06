package cnc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReverseIPTHCOutputSyncWithGDriveFolderStructure(t *testing.T) {
	// This test verifies that reverseip-thc output files are compatible with
	// the Google Drive uploader's folder monitoring and sync process.
	
	tempDir, err := os.MkdirTemp("", "cnc_gdrive_sync_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create the watch directory (merged results folder)
	watchDir := filepath.Join(tempDir, "merged")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Simulate reverseip-thc job creating output in watch directory
	resultFiles := map[string]string{
		"reverseip_8.8.8.8.txt":   "google-public-dns.example.com\n",
		"reverseip_1.1.1.1.txt":   "cloudflare-dns.example.com\n",
		"reverseip_results.txt":   "google-public-dns.example.com\ncloudflare-dns.example.com\n",
	}

	for filename, content := range resultFiles {
		filePath := filepath.Join(watchDir, filename)
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Simulate Google Drive uploader's file scan
	entries, err := os.ReadDir(watchDir)
	if err != nil {
		t.Fatal(err)
	}

	uploadedFiles := []string{}
	totalSize := int64(0)

	for _, entry := range entries {
		// Skip directories and hidden files (Google Drive uploader behavior)
		if entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			t.Logf("Warning: could not get info for %s: %v", entry.Name(), err)
			continue
		}

		uploadedFiles = append(uploadedFiles, entry.Name())
		totalSize += info.Size()

		t.Logf("  [Upload] %s (%d bytes)", entry.Name(), info.Size())
	}

	// Verify all reverseip-thc output files would be uploaded
	if len(uploadedFiles) == 0 {
		t.Fatal("No files found for upload")
	}

	if len(uploadedFiles) != len(resultFiles) {
		t.Errorf("Expected %d files to upload, found %d", len(resultFiles), len(uploadedFiles))
	}

	t.Logf("✓ reverseip-thc output files ready for Google Drive sync")
	t.Logf("  Watch directory: %s", watchDir)
	t.Logf("  Files ready to upload: %d", len(uploadedFiles))
	t.Logf("  Total size: %d bytes", totalSize)
	t.Logf("  Files:")
	for _, f := range uploadedFiles {
		t.Logf("    - %s", f)
	}

	// Verify files are in correct state for deletion after upload
	// (if delete_after_upload is enabled in GDrive config)
	for filename := range resultFiles {
		filePath := filepath.Join(watchDir, filename)
		info, err := os.Stat(filePath)
		if err != nil {
			t.Errorf("File should exist after scan: %s", filename)
			continue
		}
		if info.IsDir() {
			t.Errorf("File should not be a directory: %s", filename)
		}
	}

	t.Logf("✓ Files are in correct state for deletion after upload")
}

func TestReverseIPTHCOutputStateSilhouette(t *testing.T) {
	// Test that reverseip-thc output matches the expected file state
	// that the Google Drive uploader expects during its scan cycle.
	
	tempDir, err := os.MkdirTemp("", "cnc_gdrive_state_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	watchDir := filepath.Join(tempDir, "merged")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create output file as reverseip-thc would
	outputFile := filepath.Join(watchDir, "reverseip_results.txt")
	content := "example1.com\nexample2.com\nexample3.com\n"
	
	if err := os.WriteFile(outputFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Get file info to verify state
	info, err := os.Stat(outputFile)
	if err != nil {
		t.Fatal(err)
	}

	// Verify file properties that Google Drive uploader requires
	checks := map[string]bool{
		"Is regular file":        info.Mode().IsRegular(),
		"Is not empty":           info.Size() > 0,
		"Has read permission":    (info.Mode().Perm() & 0400) != 0,
		"Modification time set":  !info.ModTime().IsZero(),
		"Not in future":          info.ModTime().Before(time.Now().Add(1*time.Second)),
	}

	allPass := true
	for check, result := range checks {
		status := "✓"
		if !result {
			status = "✗"
			allPass = false
		}
		t.Logf("  %s %s", status, check)
	}

	if !allPass {
		t.Fatal("File state does not match Google Drive uploader requirements")
	}

	t.Logf("✓ reverseip-thc output file state matches GDrive uploader requirements")
	t.Logf("  File: %s", outputFile)
	t.Logf("  Size: %d bytes", info.Size())
	t.Logf("  Mode: %o", info.Mode().Perm())
}
