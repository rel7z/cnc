package cnc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot resolve user home dir")
	}

	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"~", home},
		{"~/merged", filepath.Join(home, "merged")},
		{"~/merged/sub", filepath.Join(home, "merged", "sub")},
		{"/var/log", "/var/log"},
		{"./relative", "./relative"},
	}

	for _, tt := range tests {
		got := ExpandPath(tt.input)
		if got != tt.expected {
			t.Errorf("ExpandPath(%q) = %q, expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestServerConfigLoadAndSave(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.json")

	// 1. Loading non-existent file should create it with defaults
	cfg, err := LoadServerConfig(configPath)
	if err == nil {
		t.Fatalf("expected error on initial load of non-existent file, got nil")
	}
	if cfg == nil {
		t.Fatalf("expected default config to be returned, got nil")
	}
	if cfg.GDrive == nil || cfg.GDrive.WatchDir != "~/merged" {
		t.Errorf("expected default GDrive.WatchDir to be '~/merged', got %v", cfg.GDrive)
	}

	// Verify file was written to disk
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("expected config file to be created on disk")
	}

	// 2. Modify config and save
	cfg.HTTPAddr = ":8888"
	cfg.GDrive.WatchDir = "~/my_custom_merged"
	if err := SaveServerConfig(configPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// 3. Load back
	loaded, err := LoadServerConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if loaded.HTTPAddr != ":8888" {
		t.Errorf("expected HTTPAddr :8888, got %s", loaded.HTTPAddr)
	}
	if loaded.GDrive == nil || loaded.GDrive.WatchDir != "~/my_custom_merged" {
		t.Errorf("expected GDrive.WatchDir '~/my_custom_merged', got %v", loaded.GDrive)
	}
}
