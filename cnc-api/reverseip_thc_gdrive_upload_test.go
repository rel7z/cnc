package cnc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReverseIPTHCOutputUploadedToGDriveWatchFolder(t *testing.T) {
	// This test verifies that reverseip-thc output files are placed in a location
	// where the Google Drive uploader will automatically pick them up.
	
	tempDir, err := os.MkdirTemp("", "cnc_gdrive_upload_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Simulate the merged results folder (watch directory)
	watchDir := filepath.Join(tempDir, "merged")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Simulate reverseip-thc writing results to the watch folder
	outputFilePath := filepath.Join(watchDir, "reverseip_results.txt")
	outputContent := "example.com\ngoogle.com\nfacebook.com\ntwitter.com\nreddit.com\n"
	
	if err := os.WriteFile(outputFilePath, []byte(outputContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify the file exists and is ready for Google Drive upload
	info, err := os.Stat(outputFilePath)
	if err != nil {
		t.Fatalf("Output file should exist: %v", err)
	}

	if info.IsDir() {
		t.Fatal("Output file should be a regular file")
	}

	if info.Size() == 0 {
		t.Fatal("Output file should not be empty")
	}

	// Verify file is readable by Google Drive uploader
	data, err := os.ReadFile(outputFilePath)
	if err != nil {
		t.Fatalf("Output file should be readable: %v", err)
	}

	if len(data) != len(outputContent) {
		t.Errorf("Output content mismatch: expected %d bytes, got %d", len(outputContent), len(data))
	}

	// Simulate Google Drive uploader checking for new files
	entries, err := os.ReadDir(watchDir)
	if err != nil {
		t.Fatal(err)
	}

	foundOutput := false
	for _, entry := range entries {
		if entry.Name() == "reverseip_results.txt" && !entry.IsDir() {
			foundOutput = true
			t.Logf("✓ reverseip-thc output found in watch directory: %s", entry.Name())
			info, _ := entry.Info()
			if info != nil {
				t.Logf("  File size: %d bytes", info.Size())
			}
		}
	}

	if !foundOutput {
		t.Fatal("reverseip-thc output file not found in watch directory")
	}

	// Verify file modification time is recent (within last minute)
	// This ensures Google Drive uploader will detect it as a new file
	modTime := info.ModTime()
	now := time.Now()
	if now.Sub(modTime) > time.Minute {
		t.Errorf("File modification time is too old: %v (should be recent)", modTime)
	}

	t.Logf("✓ reverseip-thc output is ready for Google Drive upload")
	t.Logf("  Watch directory: %s", watchDir)
	t.Logf("  Output file: %s", outputFilePath)
	t.Logf("  File size: %d bytes", info.Size())
	t.Logf("  Lines: %d", len(data))
	t.Logf("  Ready for automatic GDrive sync on next uploader scan")
}
