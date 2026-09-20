package cnc

import (
	"bytes"
	"fmt"
	"testing"
)

func TestMergeAndDeduplicateLines(t *testing.T) {
	tests := []struct {
		name     string
		sources  [][]byte
		expected string
	}{
		{
			name: "single source deduplication",
			sources: [][]byte{
				[]byte("hello world\nhello world\nfoo\n"),
			},
			expected: "hello world\nfoo\n",
		},
		{
			name: "user example: existing hello world and incoming hello world + hello123",
			sources: [][]byte{
				[]byte("hello world\n"),
				[]byte("hello world\nhello123\n"),
			},
			expected: "hello world\nhello123\n",
		},
		{
			name: "multiple sources with mixed overlaps and carriage returns",
			sources: [][]byte{
				[]byte("line1\r\nline2\r\nline3\n"),
				[]byte("line2\nline4\r\nline1\n"),
				[]byte("line5\n"),
			},
			expected: "line1\nline2\nline3\nline4\nline5\n",
		},
		{
			name:     "empty sources",
			sources:  [][]byte{[]byte(""), nil},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeAndDeduplicateLines(tt.sources...)
			if tt.expected == "" {
				if len(got) != 0 {
					t.Fatalf("expected empty output, got %q", string(got))
				}
				return
			}
			if !bytes.Equal(got, []byte(tt.expected)) {
				t.Fatalf("expected %q, got %q", tt.expected, string(got))
			}
		})
	}
}

func TestIsTextFile(t *testing.T) {
	if !isTextFile("x.txt", []byte("hello world")) {
		t.Errorf("x.txt should be recognized as text")
	}
	if !isTextFile("data.csv", []byte("a,b,c")) {
		t.Errorf("data.csv should be recognized as text")
	}
	if isTextFile("binary.bin", []byte{0x00, 0x01, 0x02, 0x00}) {
		t.Errorf("binary.bin with null bytes should not be text")
	}
}

// Simulated Drive storage to demonstrate and verify the exact lifecycle:
// 1. Initial upload creates 1 file in Drive.
// 2. Updated file with same name downloads existing, merges lines, deduplicates, and updates in-place.
// 3. Guarantees ONLY ONE file exists in Drive at all times.
// 4. Cleans up any prior duplicates from previous runs.
type SimulatedDrive struct {
	Files map[string]*SimulatedDriveFile // fileID -> file
}

type SimulatedDriveFile struct {
	ID      string
	Name    string
	Content []byte
}

func (s *SimulatedDrive) FindAllByName(name string) []*SimulatedDriveFile {
	var matches []*SimulatedDriveFile
	for _, f := range s.Files {
		if f.Name == name {
			matches = append(matches, f)
		}
	}
	return matches
}

func (s *SimulatedDrive) Count() int {
	return len(s.Files)
}

// SimulateSync simulates the syncFile algorithm
func (s *SimulatedDrive) SimulateSync(filename string, newLocalContent []byte) (localFinal []byte, driveFinal []byte) {
	existingFiles := s.FindAllByName(filename)

	if len(existingFiles) > 0 {
		primaryFile := existingFiles[0]

		var driveSources [][]byte
		for _, f := range existingFiles {
			driveSources = append(driveSources, f.Content)
		}

		// Clean up extra duplicate files so only 1 file remains in Drive
		if len(existingFiles) > 1 {
			for _, extra := range existingFiles[1:] {
				delete(s.Files, extra.ID)
			}
		}

		// Combine all Drive sources + local file, deduplicating lines & filtering empty lines
		sources := append(driveSources, newLocalContent)
		merged := mergeAndDeduplicateLines(sources...)

		// Update single Drive file in-place
		primaryFile.Content = merged
		return merged, primaryFile.Content
	}

	// File does not exist in Drive: create single new file
	deduped := mergeAndDeduplicateLines(newLocalContent)
	newID := fmt.Sprintf("drive_file_%03d", len(s.Files)+1)
	s.Files[newID] = &SimulatedDriveFile{
		ID:      newID,
		Name:    filename,
		Content: deduped,
	}
	return deduped, s.Files[newID].Content
}

func TestSimulateCleanupExistingDriveDuplicates(t *testing.T) {
	drive := &SimulatedDrive{
		Files: make(map[string]*SimulatedDriveFile),
	}

	filename := "x.txt"

	// Simulate user state: 3 duplicate files were committed to Google Drive from previous runs
	drive.Files["id_1"] = &SimulatedDriveFile{ID: "id_1", Name: filename, Content: []byte("hello world\n")}
	drive.Files["id_2"] = &SimulatedDriveFile{ID: "id_2", Name: filename, Content: []byte("hello world\nhello123\n")}
	drive.Files["id_3"] = &SimulatedDriveFile{ID: "id_3", Name: filename, Content: []byte("hello world\n")}

	if drive.Count() != 3 {
		t.Fatalf("Pre-condition failed: expected 3 files, got %d", drive.Count())
	}

	// User updates local file with "world2"
	localContent := []byte("world2\n")
	localFinal, driveFinal := drive.SimulateSync(filename, localContent)

	// Verify only 1 file remains in Google Drive
	if drive.Count() != 1 {
		t.Fatalf("Expected exactly 1 file remaining in Drive, got %d", drive.Count())
	}

	// Verify content combined all 3 drive files + local file, deduplicated, and filtered empty lines
	expected := "hello world\nhello123\nworld2\n"
	if string(driveFinal) != expected {
		t.Fatalf("Drive content mismatch.\nExpected:\n%s\nGot:\n%s", expected, string(driveFinal))
	}
	if string(localFinal) != expected {
		t.Fatalf("Local content mismatch.\nExpected:\n%s\nGot:\n%s", expected, string(localFinal))
	}

	t.Logf("✓ Verified: 3 duplicate files were reduced to %d file in Drive", drive.Count())
	t.Logf("✓ Verified: Combined & deduplicated content:\n%s", string(driveFinal))
}


