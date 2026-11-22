package cnc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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
	RedirectURL       string `json:"redirect_url"`
	WatchDir          string `json:"watch_dir"`
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	TokenPath         string `json:"token_path"`
	UploadInterval    string `json:"upload_interval"`
	ParentFolderID    string `json:"parent_folder_id"`
	StateFilePath     string `json:"state_file_path"`
	DeleteAfterUpload bool   `json:"delete_after_upload"`
}

// Serializes local stream appends with uploader snapshots and cleanup.
var watcherFileMu sync.Mutex

// GDriveUploader manages file watching and Google Drive uploads.
type GDriveUploader struct {
	syncMu         sync.Mutex
	restoreMu      sync.Mutex
	restoreStatus  DriveRestoreStatus
	restoreCancel  context.CancelFunc
	restored       map[string]string
	authMu         sync.RWMutex
	states         map[string]time.Time
	dirty          chan struct{}
	config         *GDriveConfig
	oauthCfg       *oauth2.Config
	service        *drive.Service
	uploadedMu     sync.RWMutex
	uploaded       map[string]bool
	knownFiles     map[string]bool
	uploadedCount  int64
	lastUpload     time.Time
	connectedEmail string
	ctx            context.Context
	cancelFunc     context.CancelFunc
	wg             sync.WaitGroup
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

	interval, err := time.ParseDuration(cfg.UploadInterval)
	if err != nil || interval < time.Second {
		return nil, fmt.Errorf("upload_interval must be at least 1s")
	}
	if cfg.RedirectURL == "" {
		cfg.RedirectURL = oauthRedirectURL
	}
	ctx, cancel := context.WithCancel(context.Background())

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  cfg.RedirectURL,
		Scopes:       []string{drive.DriveScope},
		Endpoint:     google.Endpoint,
	}

	u := &GDriveUploader{
		config:     cfg,
		dirty:      make(chan struct{}, 1),
		oauthCfg:   oauthCfg,
		uploaded:   make(map[string]bool),
		knownFiles: make(map[string]bool),
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

// BeginAuthorization creates a short-lived, single-use OAuth state.
func (u *GDriveUploader) BeginAuthorization() (string, string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", "", err
	}
	state := base64.RawURLEncoding.EncodeToString(bytes[:])
	u.authMu.Lock()
	defer u.authMu.Unlock()
	if u.states == nil {
		u.states = make(map[string]time.Time)
	}
	for old, expiry := range u.states {
		if time.Now().After(expiry) {
			delete(u.states, old)
		}
	}
	u.states[state] = time.Now().Add(10 * time.Minute)
	return u.oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce), state, nil
}

func (u *GDriveUploader) ConsumeState(state string) bool {
	u.authMu.Lock()
	defer u.authMu.Unlock()
	expiry, ok := u.states[state]
	delete(u.states, state)
	return ok && time.Now().Before(expiry)
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
	u.authMu.Lock()
	u.service = srv
	u.authMu.Unlock()

	// Fetch and store connected email
	u.fetchConnectedEmail()

	u.NotifyResults()
	log.Printf("[gdrive] Successfully authorized!")
	return nil
}

// IsAuthorized returns true if we have a valid token.
func (u *GDriveUploader) IsAuthorized() bool {
	u.authMu.RLock()
	defer u.authMu.RUnlock()
	return u.service != nil
}

// ConnectedEmail returns the email of the authorized account.
func (u *GDriveUploader) ConnectedEmail() string {
	u.authMu.RLock()
	defer u.authMu.RUnlock()
	return u.connectedEmail
}

// Disconnect removes the saved token and clears the service.
func (u *GDriveUploader) Disconnect() error {
	u.restoreMu.Lock()
	if u.restoreCancel != nil {
		u.restoreCancel()
	}
	u.restoreMu.Unlock()
	u.authMu.Lock()
	defer u.authMu.Unlock()
	u.service = nil
	u.connectedEmail = ""
	u.states = nil
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
	u.authMu.Lock()
	u.service = srv
	u.authMu.Unlock()

	// Fetch connected email in background
	go u.fetchConnectedEmail()

	log.Printf("[gdrive] Loaded saved token, Drive service ready")
	return nil
}

// fetchConnectedEmail gets the email of the authorized Google account.
func (u *GDriveUploader) fetchConnectedEmail() {
	u.authMu.RLock()
	service := u.service
	u.authMu.RUnlock()
	if service == nil {
		return
	}
	about, err := service.About.Get().Fields("user").Context(u.ctx).Do()
	if err != nil {
		log.Printf("[gdrive] Could not fetch user info: %v", err)
		u.markReauthorizationRequired(err)
		return
	}
	if about.User != nil {
		u.authMu.Lock()
		if u.service == service {
			u.connectedEmail = about.User.EmailAddress
		}
		u.authMu.Unlock()
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
	if err != nil || interval < time.Second {
		return fmt.Errorf("upload_interval must be at least 1s")
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

// NotifyResults coalesces stream updates without blocking worker ingestion.
func (u *GDriveUploader) NotifyResults() {
	select {
	case u.dirty <- struct{}{}:
	default:
	}
}

// watchLoop keeps periodic scans as a fallback and batches live updates.
func (u *GDriveUploader) watchLoop(interval time.Duration) {
	defer u.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var pending <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	u.scanAndUpload()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-ticker.C:
			u.scanAndUpload()
		case <-u.dirty:
			if pending == nil {
				timer = time.NewTimer(2 * time.Second)
				pending = timer.C
			}
		case <-pending:
			pending = nil
			u.scanAndUpload()
		}
	}
}

// Delete only an uploaded snapshot. New lines arriving during a network
// upload must remain available for the next sync.
func (u *GDriveUploader) removeUploadedSnapshot(path, filename string) {
	watcherFileMu.Lock()
	defer watcherFileMu.Unlock()
	hash, err := u.hashFile(path)
	if err != nil {
		return
	}
	u.uploadedMu.RLock()
	uploaded := u.uploaded[filename+":"+hash]
	u.uploadedMu.RUnlock()
	if uploaded {
		if err := processorCaptureBeforeDelete(path); err != nil {
			log.Printf("[gdrive] Retaining %s: %v", filename, err)
			return
		}
		if err := os.Remove(path); err != nil {
			log.Printf("[gdrive] Error deleting %s: %v", filename, err)
		}
	}
}

// scanAndUpload scans the watch directory and synchronizes files.
func (u *GDriveUploader) scanAndUpload() {
	u.syncMu.Lock()
	defer u.syncMu.Unlock()
	if !u.IsAuthorized() {
		log.Printf("[gdrive] Skipping scan — not authorized yet")
		return
	}
	if err := u.syncDeletions(); err != nil {
		u.markReauthorizationRequired(err)
		log.Printf("[gdrive] Could not reconcile deletions: %v", err)
		return
	}

	entries, err := os.ReadDir(u.config.WatchDir)
	if err != nil {
		log.Printf("[gdrive] Error reading directory: %v", err)
		return
	}

	for _, entry := range entries {
		if isImmutableEnumResult(entry.Name()) {
			continue
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || entry.Name()[0] == '.' {
			continue
		}

		fullPath := filepath.Join(u.config.WatchDir, entry.Name())
		if fullPath == u.config.TokenPath || fullPath == u.config.StateFilePath {
			continue
		}

		contentHash, err := u.hashFile(fullPath)
		if err != nil {
			log.Printf("[gdrive] Error hashing %s: %v", entry.Name(), err)
			continue
		}

		stateKey := entry.Name() + ":" + contentHash

		u.uploadedMu.RLock()
		restoredHash := u.restored[entry.Name()]
		alreadyUploaded := u.uploaded[stateKey]
		u.uploadedMu.RUnlock()
		// A restore is explicitly intended to keep the historical local copy.
		if restoredHash == contentHash {
			continue
		}

		if alreadyUploaded {
			if u.config.DeleteAfterUpload {
				u.removeUploadedSnapshot(fullPath, entry.Name())
			}
			continue
		}

		var syncErr error
		for attempt := 1; attempt <= 3; attempt++ {
			syncErr = u.syncFile(fullPath, entry.Name(), stateKey)
			if syncErr == nil {
				break
			}
			u.markReauthorizationRequired(syncErr)
			log.Printf("[gdrive] Warning: failed to sync %s (attempt %d/3): %v", entry.Name(), attempt, syncErr)
			if !u.IsAuthorized() {
				break
			}
			select {
			case <-u.ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		if syncErr != nil {
			log.Printf("[gdrive] Error syncing %s after 3 attempts: %v", entry.Name(), syncErr)
			if !u.IsAuthorized() {
				return
			}
			continue
		}

		if err := u.saveState(); err != nil {
			log.Printf("[gdrive] Warning: could not save state: %v", err)
		}

		if u.config.DeleteAfterUpload {
			u.removeUploadedSnapshot(fullPath, entry.Name())
		}
	}
}

// syncDeletions mirrors file removals for filenames that have previously been
// synchronized. It only acts on known pairs, so it never deletes Drive-only
// files that CNC has never managed. Drive files are moved to Trash, and a local
// copy deleted from Drive is moved beneath a hidden recovery directory.
func (u *GDriveUploader) syncDeletions() error {
	u.authMu.RLock()
	defer u.authMu.RUnlock()
	if u.service == nil {
		return fmt.Errorf("Google Drive is disconnected")
	}

	u.uploadedMu.RLock()
	names := make([]string, 0, len(u.knownFiles))
	for name := range u.knownFiles {
		names = append(names, name)
	}
	u.uploadedMu.RUnlock()
	sort.Strings(names)

	for _, name := range names {
		if isImmutableEnumResult(name) {
			continue
		}
		u.uploadedMu.RLock()
		_, restored := u.restored[name]
		u.uploadedMu.RUnlock()
		if restored {
			continue
		}
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
			continue
		}

		localPath := filepath.Join(u.config.WatchDir, name)
		info, statErr := os.Lstat(localPath)
		localExists := statErr == nil
		if statErr != nil && !os.IsNotExist(statErr) {
			return fmt.Errorf("check local file %s: %w", name, statErr)
		}
		if localExists && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			continue
		}

		driveFiles, err := u.findDriveFilesByName(name)
		if err != nil {
			return fmt.Errorf("check Drive file %s: %w", name, err)
		}

		switch {
		case localExists && len(driveFiles) == 0:
			moved, err := u.moveLocalFileToRecovery(localPath, name)
			if err != nil {
				return fmt.Errorf("move locally deleted Drive file %s to recovery: %w", name, err)
			}
			if moved {
				log.Printf("[gdrive] Drive deletion mirrored locally: %s", name)
			}
			if err := u.forgetSyncedFile(name); err != nil {
				return err
			}

		case !localExists && len(driveFiles) > 0 && !u.config.DeleteAfterUpload:
			for _, remote := range driveFiles {
				if _, err := u.service.Files.Update(remote.Id, &drive.File{Trashed: true}).
					SupportsAllDrives(true).Context(u.ctx).Do(); err != nil {
					return fmt.Errorf("move deleted local file %s to Drive Trash: %w", name, err)
				}
			}
			log.Printf("[gdrive] Local deletion mirrored to Drive Trash: %s", name)
			if err := u.forgetSyncedFile(name); err != nil {
				return err
			}

		case !localExists && len(driveFiles) == 0:
			if err := u.forgetSyncedFile(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func (u *GDriveUploader) moveLocalFileToRecovery(path, filename string) (bool, error) {
	watcherFileMu.Lock()
	defer watcherFileMu.Unlock()

	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("refusing to move non-regular file")
	}
	recoveryDir := filepath.Join(u.config.WatchDir, ".cnc-deleted", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := os.MkdirAll(recoveryDir, 0700); err != nil {
		return false, err
	}
	if err := os.Rename(path, filepath.Join(recoveryDir, filename)); err != nil {
		return false, err
	}
	return true, nil
}

func (u *GDriveUploader) forgetSyncedFile(filename string) error {
	u.uploadedMu.Lock()
	delete(u.knownFiles, filename)
	prefix := filename + ":"
	for key := range u.uploaded {
		if strings.HasPrefix(key, prefix) {
			delete(u.uploaded, key)
		}
	}
	u.uploadedMu.Unlock()
	return u.saveState()
}

func (u *GDriveUploader) rememberSyncedFile(filename string) {
	u.uploadedMu.Lock()
	if u.knownFiles == nil {
		u.knownFiles = make(map[string]bool)
	}
	u.knownFiles[filename] = true
	u.uploadedMu.Unlock()
}

func (u *GDriveUploader) markReauthorizationRequired(err error) {
	if err == nil {
		return
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "invalid_grant") && !strings.Contains(message, "token has been expired or revoked") {
		return
	}
	u.authMu.Lock()
	if u.service != nil {
		u.service = nil
		u.connectedEmail = ""
		log.Printf("[gdrive] Google authorization expired or revoked; reconnect from Google Drive Settings")
	}
	u.authMu.Unlock()
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
			line := cleanEnumTextLine(strings.TrimRight(scanner.Text(), "\r"))
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
	parent := u.config.ParentFolderID
	if parent == "" {
		parent = "root"
	}
	{
		escapedParent := strings.ReplaceAll(parent, "'", "\\'")
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

// syncFile uploads a stable local snapshot merged with Drive history.
// Never rewrite the actively appended local file with an older snapshot.
func (u *GDriveUploader) syncFile(path, filename, initialStateKey string) error {
	u.authMu.RLock()
	defer u.authMu.RUnlock()
	if u.service == nil {
		return fmt.Errorf("Google Drive is disconnected")
	}
	if isImmutableEnumResult(filename) || isInternetMapBundle(filename) {
		if err := u.syncImmutableEnumResult(path, filename); err != nil {
			return err
		}
		u.rememberSyncedFile(filename)
		return nil
	}
	watcherFileMu.Lock()
	localBytes, err := os.ReadFile(path)
	watcherFileMu.Unlock()
	if err != nil {
		return fmt.Errorf("read local file: %w", err)
	}

	// Record the bytes actually uploaded, not an earlier scan hash.
	initialStateKey = filename + ":" + sha256Bytes(localBytes)
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
		u.rememberSyncedFile(filename)

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
		u.rememberSyncedFile(filename)

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

type persistedGDriveState struct {
	Version    int               `json:"version"`
	Uploaded   []string          `json:"uploaded"`
	KnownFiles []string          `json:"known_files"`
	Restored   map[string]string `json:"restored,omitempty"`
}

// loadState loads uploaded hashes and the filenames that have been paired
// across Drive and the watch directory. Legacy hash-array state is migrated by
// extracting each filename prefix from its stored filename:sha256 key.
func (u *GDriveUploader) loadState() error {
	data, err := os.ReadFile(u.config.StateFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state persistedGDriveState
	var legacyHashes []string
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &legacyHashes); err != nil {
			return err
		}
		state.Uploaded = legacyHashes
	} else if err := json.Unmarshal(trimmed, &state); err != nil {
		return err
	}

	u.uploadedMu.Lock()
	defer u.uploadedMu.Unlock()
	u.restored = state.Restored
	for _, h := range state.Uploaded {
		u.uploaded[h] = true
		if i := strings.LastIndexByte(h, ':'); i > 0 && len(h)-i-1 == 64 {
			filename := h[:i]
			if filename != "" && filepath.Base(filename) == filename && !strings.HasPrefix(filename, ".") {
				u.knownFiles[filename] = true
			}
		}
	}
	for _, filename := range state.KnownFiles {
		if filename != "" && filepath.Base(filename) == filename && !strings.HasPrefix(filename, ".") {
			u.knownFiles[filename] = true
		}
	}

	log.Printf("[gdrive] Loaded %d previously uploaded file hashes and %d synchronized filenames", len(state.Uploaded), len(u.knownFiles))
	return nil
}

// saveState saves uploaded file hashes to disk.
func (u *GDriveUploader) saveState() error {
	if err := os.MkdirAll(filepath.Dir(u.config.StateFilePath), 0700); err != nil {
		return err
	}
	u.uploadedMu.RLock()
	state := persistedGDriveState{Version: 1, Restored: make(map[string]string)}
	for name, digest := range u.restored {
		state.Restored[name] = digest
	}
	for h := range u.uploaded {
		state.Uploaded = append(state.Uploaded, h)
	}
	for name := range u.knownFiles {
		state.KnownFiles = append(state.KnownFiles, name)
	}
	u.uploadedMu.RUnlock()
	sort.Strings(state.Uploaded)
	sort.Strings(state.KnownFiles)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmp := u.config.StateFilePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, u.config.StateFilePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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
		ConnectedEmail: u.ConnectedEmail(),
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
		restored := u.restored[entry.Name()] == contentHash
		u.uploadedMu.RUnlock()
		if !already && !restored {
			pending++
		}
	}

	return pending
}
