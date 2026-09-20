package cnc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

const oauthRedirectURL = "http://localhost:8080/api/config/gdrive/callback"

// GDriveConfig holds configuration for Google Drive integration.
type GDriveConfig struct {
	WatchDir          string `json:"watch_dir"`
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	TokenPath         string `json:"token_path"`
	UploadInterval    string `json:"upload_interval"`
	ParentFolderID    string `json:"parent_folder_id"`
	StateFilePath     string `json:"state_file_path"`
	DeleteAfterUpload bool   `json:"delete_after_upload"`
}

// GDriveUploader manages file watching and Google Drive uploads.
type GDriveUploader struct {
	config        *GDriveConfig
	oauthCfg      *oauth2.Config
	service       *drive.Service
	uploadedMu    sync.RWMutex
	uploaded      map[string]bool
	uploadedCount int64
	lastUpload    time.Time
	connectedEmail string
	ctx           context.Context
	cancelFunc    context.CancelFunc
	wg            sync.WaitGroup
}

// NewGDriveUploader creates a new Google Drive uploader instance.
func NewGDriveUploader(cfg *GDriveConfig) (*GDriveUploader, error) {
	if cfg.WatchDir == "" {
		return nil, fmt.Errorf("watch_dir is required")
	}
	cfg.WatchDir = ExpandPath(cfg.WatchDir)
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("client_id and client_secret are required")
	}
	if cfg.UploadInterval == "" {
		cfg.UploadInterval = "30s"
	}
	if cfg.StateFilePath == "" {
		cfg.StateFilePath = filepath.Join(cfg.WatchDir, ".gdrive_state.json")
	} else {
		cfg.StateFilePath = ExpandPath(cfg.StateFilePath)
	}
	if cfg.TokenPath == "" {
		cfg.TokenPath = filepath.Join(cfg.WatchDir, ".gdrive_token.json")
	} else {
		cfg.TokenPath = ExpandPath(cfg.TokenPath)
	}

	ctx, cancel := context.WithCancel(context.Background())

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  oauthRedirectURL,
		Scopes:       []string{drive.DriveScope},
		Endpoint:     google.Endpoint,
	}

	u := &GDriveUploader{
		config:     cfg,
		oauthCfg:   oauthCfg,
		uploaded:   make(map[string]bool),
		ctx:        ctx,
		cancelFunc: cancel,
	}

	if err := u.loadState(); err != nil {
		log.Printf("[gdrive] Warning: could not load state: %v", err)
	}

	// Try to init service from saved token
	if err := u.initFromSavedToken(); err != nil {
		// Not an error — just means not authorized yet
		log.Printf("[gdrive] Not authorized yet. Use the UI to connect your Google account.")
	}

	return u, nil
}

// GetAuthURL returns the OAuth2 authorization URL for the user to visit.
func (u *GDriveUploader) GetAuthURL() string {
	return u.oauthCfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

// ExchangeCode exchanges an OAuth2 authorization code for a token and saves it.
func (u *GDriveUploader) ExchangeCode(code string) error {
	token, err := u.oauthCfg.Exchange(u.ctx, code)
	if err != nil {
		return fmt.Errorf("failed to exchange code: %w", err)
	}

	if err := u.saveToken(token); err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	client := u.oauthCfg.Client(u.ctx, token)
	srv, err := drive.NewService(u.ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create drive service: %w", err)
	}
	u.service = srv

	// Fetch and store connected email
	u.fetchConnectedEmail()

	log.Printf("[gdrive] Successfully authorized!")
	return nil
}

// IsAuthorized returns true if we have a valid token.
func (u *GDriveUploader) IsAuthorized() bool {
	return u.service != nil
}

// ConnectedEmail returns the email of the authorized account.
func (u *GDriveUploader) ConnectedEmail() string {
	return u.connectedEmail
}

// Disconnect removes the saved token and clears the service.
func (u *GDriveUploader) Disconnect() error {
	u.service = nil
	u.connectedEmail = ""
	if err := os.Remove(u.config.TokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	log.Printf("[gdrive] Disconnected")
	return nil
}

// initFromSavedToken loads a saved token and initializes the Drive service.
func (u *GDriveUploader) initFromSavedToken() error {
	token, err := u.loadToken()
	if err != nil {
		return err
	}

	client := u.oauthCfg.Client(u.ctx, token)
	srv, err := drive.NewService(u.ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create drive service: %w", err)
	}
	u.service = srv

	// Fetch connected email in background
	go u.fetchConnectedEmail()

	log.Printf("[gdrive] Loaded saved token, Drive service ready")
	return nil
}

// fetchConnectedEmail gets the email of the authorized Google account.
func (u *GDriveUploader) fetchConnectedEmail() {
	if u.service == nil {
		return
	}
	about, err := u.service.About.Get().Fields("user").Context(u.ctx).Do()
	if err != nil {
		log.Printf("[gdrive] Could not fetch user info: %v", err)
		return
	}
	if about.User != nil {
		u.connectedEmail = about.User.EmailAddress
		log.Printf("[gdrive] Connected as: %s", u.connectedEmail)
	}
}

// saveToken saves an OAuth2 token to disk.
func (u *GDriveUploader) saveToken(token *oauth2.Token) error {
	if err := os.MkdirAll(filepath.Dir(u.config.TokenPath), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return os.WriteFile(u.config.TokenPath, data, 0600)
}

// loadToken loads an OAuth2 token from disk.
func (u *GDriveUploader) loadToken() (*oauth2.Token, error) {
	data, err := os.ReadFile(u.config.TokenPath)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// Start begins watching the directory and uploading files.
func (u *GDriveUploader) Start() error {
	if err := os.MkdirAll(u.config.WatchDir, 0755); err != nil {
		return fmt.Errorf("failed to create watch directory: %w", err)
	}

	interval, err := time.ParseDuration(u.config.UploadInterval)
	if err != nil {
		return fmt.Errorf("invalid upload_interval: %w", err)
	}

	log.Printf("[gdrive] Started watching %s (scanning every %s)", u.config.WatchDir, interval)

	u.wg.Add(1)
	go u.watchLoop(interval)

	return nil
}

// Stop gracefully stops the uploader.
func (u *GDriveUploader) Stop() {
	log.Println("[gdrive] Stopping...")
	u.cancelFunc()
	u.wg.Wait()

	if err := u.saveState(); err != nil {
		log.Printf("[gdrive] Warning: could not save state: %v", err)
	}

	log.Println("[gdrive] Stopped")
}

// watchLoop periodically scans the watch directory for new files.
func (u *GDriveUploader) watchLoop(interval time.Duration) {
	defer u.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial scan immediately
	u.scanAndUpload()

	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			u.scanAndUpload()
		}
	}
}

// scanAndUpload scans the watch directory and synchronizes files.
func (u *GDriveUploader) scanAndUpload() {
	if !u.IsAuthorized() {
		log.Printf("[gdrive] Skipping scan — not authorized yet")
		return
	}

	entries, err := os.ReadDir(u.config.WatchDir)
	if err != nil {
		log.Printf("[gdrive] Error reading directory: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}

		fullPath := filepath.Join(u.config.WatchDir, entry.Name())

		contentHash, err := u.hashFile(fullPath)
		if err != nil {
			log.Printf("[gdrive] Error hashing %s: %v", entry.Name(), err)
			continue
		}
		
		stateKey := entry.Name() + ":" + contentHash

		u.uploadedMu.RLock()
		alreadyUploaded := u.uploaded[stateKey]
		u.uploadedMu.RUnlock()

		if alreadyUploaded {
			if u.config.DeleteAfterUpload {
				os.Remove(fullPath) //nolint:errcheck
			}
			continue
		}

		var syncErr error
		for attempt := 1; attempt <= 3; attempt++ {
			syncErr = u.syncFile(fullPath, entry.Name(), stateKey)
			if syncErr == nil {
				break
			}
			log.Printf("[gdrive] Warning: failed to sync %s (attempt %d/3): %v", entry.Name(), attempt, syncErr)
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		if syncErr != nil {
			log.Printf("[gdrive] Error syncing %s after 3 attempts: %v", entry.Name(), syncErr)
			continue
		}

		if err := u.saveState(); err != nil {
			log.Printf("[gdrive] Warning: could not save state: %v", err)
		}

		if u.config.DeleteAfterUpload {
			if err := os.Remove(fullPath); err != nil {
				log.Printf("[gdrive] Error deleting %s: %v", entry.Name(), err)
			} else {
				log.Printf("[gdrive] Deleted local file: %s", entry.Name())
			}
		}
	}
}

// isTextFile determines whether a file should be treated as line-based text.
func isTextFile(filename string, data []byte) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".csv", ".log", ".jsonl", ".lst", ".tsv", ".json", ".md", ".yaml", ".yml":
		return true
	}
	n := len(data)
	if n > 1024 {
		n = 1024
	}
	for i := 0; i < n; i++ {
		if data[i] == 0 {
			return false
		}
	}
	return true
}

// mergeAndDeduplicateLines merges lines from multiple byte slices, preserving
// line order and deduplicating lines. Empty and whitespace-only lines are filtered out.
func mergeAndDeduplicateLines(sources ...[]byte) []byte {
	seen := make(map[string]bool)
	var mergedLines []string

	for _, data := range sources {
		if len(data) == 0 {
			continue
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimRight(scanner.Text(), "\r")
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if !seen[line] {
				seen[line] = true
				mergedLines = append(mergedLines, line)
			}
		}
	}

	if len(mergedLines) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for _, line := range mergedLines {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

// sha256Bytes returns the hex-encoded SHA256 sum of a byte slice.
func sha256Bytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// findDriveFilesByName searches for all existing files with the given name in Drive.
func (u *GDriveUploader) findDriveFilesByName(filename string) ([]*drive.File, error) {
	escapedName := strings.ReplaceAll(filename, "'", "\\'")
	query := fmt.Sprintf("name = '%s' and trashed = false", escapedName)
	if u.config.ParentFolderID != "" {
		escapedParent := strings.ReplaceAll(u.config.ParentFolderID, "'", "\\'")
		query += fmt.Sprintf(" and '%s' in parents", escapedParent)
	}

	list, err := u.service.Files.List().
		Q(query).
		Fields("files(id, name, description)").
		SupportsAllDrives(true).
		IncludeItemsFromAllDrives(true).
		PageSize(50).
		Context(u.ctx).
		Do()
	if err != nil {
		return nil, err
	}

	return list.Files, nil
}

// downloadDriveFileContent downloads raw content of a file from Google Drive.
func (u *GDriveUploader) downloadDriveFileContent(fileID string) ([]byte, error) {
	resp, err := u.service.Files.Get(fileID).
		SupportsAllDrives(true).
		Context(u.ctx).
		Download()
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// syncFile handles two-way synchronous merging, deduplicating lines and updating both local and Drive files.
func (u *GDriveUploader) syncFile(path, filename, initialStateKey string) error {
	localBytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	isText := isTextFile(filename, localBytes)

	// Check if this file already exists in Google Drive (may have duplicates from previous runs)
	existingDriveFiles, err := u.findDriveFilesByName(filename)
	if err != nil {
		return fmt.Errorf("find files in drive: %w", err)
	}

	var finalBytes []byte

	if len(existingDriveFiles) > 0 {
		// Target primary file to update
		primaryFile := existingDriveFiles[0]

		if isText {
			// Collect contents from all existing Drive copies (in case duplicates were uploaded before)
			var driveSources [][]byte
			for _, df := range existingDriveFiles {
				remoteBytes, err := u.downloadDriveFileContent(df.Id)
				if err != nil {
					log.Printf("[gdrive] Warning: could not download %s (%s) from Drive: %v", filename, df.Id, err)
				} else if len(remoteBytes) > 0 {
					driveSources = append(driveSources, remoteBytes)
				}
			}

			// Clean up extra duplicate files on Google Drive so ONLY ONE file remains
			if len(existingDriveFiles) > 1 {
				log.Printf("[gdrive] Found %d duplicate files for %q in Drive; merging and cleaning up duplicates...", len(existingDriveFiles), filename)
				for _, extra := range existingDriveFiles[1:] {
					if err := u.service.Files.Delete(extra.Id).SupportsAllDrives(true).Context(u.ctx).Do(); err != nil {
						log.Printf("[gdrive] Warning: failed to delete duplicate Drive file %s (%s): %v", filename, extra.Id, err)
					} else {
						log.Printf("[gdrive] Removed duplicate Drive file %s (%s)", filename, extra.Id)
					}
				}
			}

			// Combine all Drive sources + local file, deduplicating lines
			sources := append(driveSources, localBytes)
			finalBytes = mergeAndDeduplicateLines(sources...)
		} else {
			finalBytes = localBytes
			// For binary files, also clean up extra duplicates if any
			if len(existingDriveFiles) > 1 {
				for _, extra := range existingDriveFiles[1:] {
					u.service.Files.Delete(extra.Id).SupportsAllDrives(true).Context(u.ctx).Do() //nolint:errcheck
				}
			}
		}

		finalHash := sha256Bytes(finalBytes)
		finalStateKey := filename + ":" + finalHash

		// Synchronize local file if content differs
		if !bytes.Equal(finalBytes, localBytes) {
			if err := os.WriteFile(path, finalBytes, 0644); err != nil {
				log.Printf("[gdrive] Warning: could not update local file %s: %v", filename, err)
			} else {
				log.Printf("[gdrive] Updated local file %s with merged & deduplicated lines", filename)
			}
		}

		// Update the single primary file in Google Drive in-place
		fileMeta := &drive.File{
			Description: fmt.Sprintf("SHA256: %s", finalHash),
		}
		_, err = u.service.Files.Update(primaryFile.Id, fileMeta).
			SupportsAllDrives(true).
			Media(bytes.NewReader(finalBytes)).
			Context(u.ctx).
			Do()
		if err != nil {
			return fmt.Errorf("update drive file %s: %w", filename, err)
		}

		u.uploadedMu.Lock()
		u.uploaded[initialStateKey] = true
		u.uploaded[finalStateKey] = true
		u.uploadedCount++
		u.lastUpload = time.Now()
		u.uploadedMu.Unlock()

		log.Printf("[gdrive] ✓ Synchronized & updated Drive file: %s (id: %s, hash: %s)", filename, primaryFile.Id, finalHash[:12])
	} else {
		// File does not exist on Drive: deduplicate lines locally and create in Drive
		if isText {
			finalBytes = mergeAndDeduplicateLines(localBytes)
		} else {
			finalBytes = localBytes
		}

		finalHash := sha256Bytes(finalBytes)
		finalStateKey := filename + ":" + finalHash

		// Update local file if deduplication changed content
		if !bytes.Equal(finalBytes, localBytes) {
			if err := os.WriteFile(path, finalBytes, 0644); err != nil {
				log.Printf("[gdrive] Warning: could not update local file %s: %v", filename, err)
			} else {
				log.Printf("[gdrive] Deduplicated local file: %s", filename)
			}
		}

		fileMeta := &drive.File{
			Name:        filename,
			Description: fmt.Sprintf("SHA256: %s", finalHash),
		}
		if u.config.ParentFolderID != "" {
			fileMeta.Parents = []string{u.config.ParentFolderID}
		}

		createdFile, err := u.service.Files.Create(fileMeta).
			SupportsAllDrives(true).
			Media(bytes.NewReader(finalBytes)).
			Context(u.ctx).
			Do()
		if err != nil {
			return fmt.Errorf("create drive file %s: %w", filename, err)
		}

		u.uploadedMu.Lock()
		u.uploaded[initialStateKey] = true
		u.uploaded[finalStateKey] = true
		u.uploadedCount++
		u.lastUpload = time.Now()
		u.uploadedMu.Unlock()

		log.Printf("[gdrive] ✓ Created & synchronized Drive file: %s (id: %s, hash: %s)", filename, createdFile.Id, finalHash[:12])
	}

	return nil
}


// hashFile computes SHA256 hash of a file.
func (u *GDriveUploader) hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// loadState loads previously uploaded file hashes from disk.
func (u *GDriveUploader) loadState() error {
	data, err := os.ReadFile(u.config.StateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var hashes []string
	if err := json.Unmarshal(data, &hashes); err != nil {
		return err
	}

	u.uploadedMu.Lock()
	defer u.uploadedMu.Unlock()
	for _, h := range hashes {
		u.uploaded[h] = true
	}

	log.Printf("[gdrive] Loaded %d previously uploaded file hashes", len(hashes))
	return nil
}

// saveState saves uploaded file hashes to disk.
func (u *GDriveUploader) saveState() error {
	u.uploadedMu.RLock()
	hashes := make([]string, 0, len(u.uploaded))
	for h := range u.uploaded {
		hashes = append(hashes, h)
	}
	u.uploadedMu.RUnlock()

	data, err := json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(u.config.StateFilePath, data, 0644)
}

// GDriveStats holds statistics about Google Drive uploads.
type GDriveStats struct {
	Enabled        bool      `json:"enabled"`
	Authorized     bool      `json:"authorized"`
	ConnectedEmail string    `json:"connected_email,omitempty"`
	Uploaded       int64     `json:"uploaded"`
	Pending        int       `json:"pending"`
	LastUpload     time.Time `json:"last_upload,omitempty"`
}

// GetStats returns current Google Drive upload statistics.
func (u *GDriveUploader) GetStats() GDriveStats {
	u.uploadedMu.RLock()
	uploaded := u.uploadedCount
	lastUpload := u.lastUpload
	u.uploadedMu.RUnlock()

	return GDriveStats{
		Enabled:        true,
		Authorized:     u.IsAuthorized(),
		ConnectedEmail: u.connectedEmail,
		Uploaded:       uploaded,
		Pending:        u.countPendingFiles(),
		LastUpload:     lastUpload,
	}
}

// countPendingFiles counts files in watch dir not yet uploaded.
func (u *GDriveUploader) countPendingFiles() int {
	entries, err := os.ReadDir(u.config.WatchDir)
	if err != nil {
		return 0
	}

	pending := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Name()[0] == '.' {
			continue
		}
		fullPath := filepath.Join(u.config.WatchDir, entry.Name())
		contentHash, err := u.hashFile(fullPath)
		if err != nil {
			continue
		}
		stateKey := entry.Name() + ":" + contentHash
		u.uploadedMu.RLock()
		already := u.uploaded[stateKey]
		u.uploadedMu.RUnlock()
		if !already {
			pending++
		}
	}

	return pending
}
