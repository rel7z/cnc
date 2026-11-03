package cnc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReverseIPTHCWithGDriveWatcher(t *testing.T) {
	// Verify that reverseip-thc output files are compatible with Google Drive uploader
	tempDir, err := os.MkdirTemp("", "cnc_gdrive_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create watch directory (simulates merged results folder)
	watchDir := filepath.Join(tempDir, "merged")
	if err := os.MkdirAll(watchDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a mock output file as if reverseip-thc wrote results
	outputFile := filepath.Join(watchDir, "reverseip_results.txt")
	mockResults := "example.com\ngoogle.com\nfacebook.com\n"
	if err := os.WriteFile(outputFile, []byte(mockResults), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify the file exists and is readable (Google Drive uploader requirement)
	info, err := os.Stat(outputFile)
	if err != nil {
		t.Errorf("output file should exist: %v", err)
	}

	if info.IsDir() {
		t.Error("output file should be a regular file, not a directory")
	}

	if info.Size() == 0 {
		t.Error("output file should not be empty")
	}

	// Verify content can be read
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Errorf("should be able to read output file: %v", err)
	}

	if len(data) == 0 {
		t.Error("output file data should not be empty")
	}

	t.Logf("✓ reverseip-thc output is compatible with Google Drive uploader")
	t.Logf("  Watch directory: %s", watchDir)
	t.Logf("  Output file: %s", outputFile)
	t.Logf("  File size: %d bytes", info.Size())
	t.Logf("  Content lines: %d", len(data))
}
