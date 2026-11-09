package cnc

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

func (s *Server) configFile() string {
	if s.config.configPath != "" {
		return s.config.configPath
	}
	return "server_config.json"
}

func (s *Server) driveConfig() *GDriveWrapper {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.config.GDrive == nil {
		return nil
	}
	cfg := *s.config.GDrive
	return &cfg
}

func (s *Server) driveUploader() *GDriveUploader {
	s.gdriveMu.RLock()
	defer s.gdriveMu.RUnlock()
	return s.gdriveUploader
}

func validateDriveConfig(cfg *GDriveWrapper) error {
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.WatchDir = strings.TrimSpace(cfg.WatchDir)
	cfg.UploadInterval = strings.TrimSpace(cfg.UploadInterval)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.ParentFolderID = strings.TrimSpace(cfg.ParentFolderID)
	if cfg.WatchDir == "" {
		cfg.WatchDir = "~/merged"
	}
	if cfg.UploadInterval == "" {
		cfg.UploadInterval = "30s"
	}
	interval, err := time.ParseDuration(cfg.UploadInterval)
	if err != nil || interval < time.Second {
		return fmt.Errorf("Upload interval must be at least 1s (for example 30s or 1m)")
	}
	if cfg.Enabled || cfg.ClientID != "" || cfg.ClientSecret != "" {
		if cfg.ClientID == "" || cfg.ClientSecret == "" {
			return fmt.Errorf("Enter both OAuth Client ID and Client Secret")
		}
		if cfg.RedirectURL == "" {
			return fmt.Errorf("Enter the OAuth Redirect URL shown in the dashboard")
		}
	}
	if cfg.RedirectURL != "" {
		u, err := url.Parse(cfg.RedirectURL)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/config/gdrive/callback" {
			return fmt.Errorf("Redirect URL must end with /api/config/gdrive/callback and have no query or fragment")
		}
		host := u.Hostname()
		ip := net.ParseIP(host)
		loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
		if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
			return fmt.Errorf("Google requires HTTPS for public redirect URLs. Use the localhost dashboard over an SSH tunnel for HTTP authorization")
		}
		if ip != nil && !loopback {
			return fmt.Errorf("Google requires a domain for public redirect URLs. Use the localhost dashboard over an SSH tunnel")
		}
	}
	if strings.Contains(cfg.ParentFolderID, "/") {
		return fmt.Errorf("Enter the Drive folder ID, not its full URL")
	}
	return nil
}

func buildDriveUploader(cfg *GDriveWrapper) (*GDriveUploader, error) {
	return NewGDriveUploader(&GDriveConfig{
		WatchDir: cfg.WatchDir, ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret,
		RedirectURL: cfg.RedirectURL, TokenPath: cfg.TokenPath, UploadInterval: cfg.UploadInterval,
		ParentFolderID: cfg.ParentFolderID, StateFilePath: cfg.StateFilePath, DeleteAfterUpload: cfg.DeleteAfterUpload,
	})
}

// driveAuthorizationBridgeURL moves OAuth to the configured callback origin
// before Google is opened. This lets a dashboard on a public host use a
// localhost callback without requiring a second dashboard login there.
func driveAuthorizationBridgeURL(redirectURL, authorizationURL, state string) (string, error) {
	callback, err := url.Parse(redirectURL)
	if err != nil || callback.Scheme == "" || callback.Host == "" {
		return "", fmt.Errorf("invalid OAuth redirect URL")
	}
	bridge := &url.URL{Scheme: callback.Scheme, Host: callback.Host, Path: "/drive-connect"}
	query := bridge.Query()
	query.Set("authorization", authorizationURL)
	query.Set("state", state)
	bridge.RawQuery = query.Encode()
	return bridge.String(), nil
}
