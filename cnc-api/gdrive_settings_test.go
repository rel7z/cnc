package cnc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func driveSettingsServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	cfg := DefaultServerConfig()
	cfg.DataDir = dir
	path := filepath.Join(dir, "custom-config.json")
	if err := SaveServerConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadServerConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(loaded)
	t.Cleanup(func() {
		if u := s.driveUploader(); u != nil {
			u.Stop()
		}
	})
	return s
}

func TestDriveSettingsApplyWithoutRestart(t *testing.T) {
	s := driveSettingsServer(t)
	cfg := GDriveWrapper{Enabled: true, WatchDir: filepath.Join(t.TempDir(), "watch"), ClientID: "test.apps.googleusercontent.com", ClientSecret: "test-secret", UploadInterval: "1s", RedirectURL: "http://127.0.0.1:3400/api/config/gdrive/callback"}
	save := func(cfg GDriveWrapper) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(cfg)
		w := httptest.NewRecorder()
		s.handleUpdateGDriveConfig(w, httptest.NewRequest("POST", "/api/config/gdrive", strings.NewReader(string(body))))
		return w
	}
	if w := save(cfg); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.driveUploader() == nil {
		t.Fatal("save did not initialize uploader")
	}
	status := httptest.NewRecorder()
	s.handleGDriveAuthStatus(status, httptest.NewRequest("GET", "/api/config/gdrive/auth-status", nil))
	if !strings.Contains(status.Body.String(), `"configured":true`) {
		t.Fatal(status.Body.String())
	}
	disk, err := LoadServerConfig(s.configFile())
	if err != nil {
		t.Fatal(err)
	}
	if !disk.GDrive.Enabled || disk.GDrive.ClientSecret != "test-secret" {
		t.Fatal("custom config path not persisted")
	}
	info, _ := os.Stat(s.configFile())
	if info.Mode().Perm() != 0600 {
		t.Fatal("configuration is not private")
	}
	get := httptest.NewRecorder()
	s.handleGetGDriveConfig(get, httptest.NewRequest("GET", "/api/config/gdrive", nil))
	if strings.Contains(get.Body.String(), "test-secret") || !strings.Contains(get.Body.String(), `"client_secret_set":true`) {
		t.Fatal("secret not redacted")
	}
	// Redacted secret can be preserved, and disabling stops the upload loop while keeping Connect available.
	old := s.driveUploader()
	cfg.ClientSecret = ""
	cfg.Enabled = false
	if w := save(cfg); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if old.ctx.Err() == nil {
		t.Fatal("old watcher not stopped")
	}
	if s.driveConfig().ClientSecret != "test-secret" || s.driveUploader() == nil {
		t.Fatal("secret or authorization setup lost")
	}
	cfg.Enabled = true
	cfg.UploadInterval = "0s"
	if w := save(cfg); w.Code != 400 {
		t.Fatal("zero interval accepted")
	}
	if s.driveConfig().Enabled {
		t.Fatal("invalid save altered live configuration")
	}
	previousPath := s.driveConfig().TokenPath
	cfg.Enabled = false
	cfg.UploadInterval = "1s"
	cfg.ClientID = "other-client"
	cfg.ClientSecret = "other-secret"
	cfg.TokenPath = previousPath
	if w := save(cfg); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.driveConfig().TokenPath == previousPath {
		t.Fatal("changed OAuth client reused previous token path")
	}
}

func TestDriveSyncRequiresAuthorization(t *testing.T) {
	s := driveSettingsServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := httptest.NewRecorder()
		s.handleGDriveSync(w, httptest.NewRequest(method, "/api/config/gdrive/sync", nil))
		want := http.StatusMethodNotAllowed
		if method == http.MethodPost {
			want = http.StatusConflict
		}
		if w.Code != want {
			t.Fatalf("%s: got %d, want %d", method, w.Code, want)
		}
	}
}

func TestDriveSyncQueuesUploader(t *testing.T) {
	s := driveSettingsServer(t)
	cfg := &GDriveWrapper{Enabled: true, WatchDir: t.TempDir(), ClientID: "test-client", ClientSecret: "test-secret", UploadInterval: "30s", RedirectURL: "http://localhost:8081/api/config/gdrive/callback"}
	uploader, err := buildDriveUploader(cfg)
	if err != nil {
		t.Fatal(err)
	}
	uploader.authMu.Lock()
	uploader.service = &drive.Service{}
	uploader.authMu.Unlock()
	s.config.GDrive = cfg
	s.gdriveUploader = uploader
	w := httptest.NewRecorder()
	s.handleGDriveSync(w, httptest.NewRequest(http.MethodPost, "/api/config/gdrive/sync", nil))
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"queued"`) {
		t.Fatalf("sync response: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-uploader.dirty:
	default:
		t.Fatal("sync did not wake uploader")
	}
}

func TestDriveRedirectValidation(t *testing.T) {
	for _, tc := range []struct {
		redirect string
		valid    bool
	}{
		{"http://localhost:3400/api/config/gdrive/callback", true},
		{"http://127.0.0.1:3400/api/config/gdrive/callback", true},
		{"https://dashboard.example.com/api/config/gdrive/callback", true},
		{"http://172.236.141.170/api/config/gdrive/callback", false},
		{"https://172.236.141.170/api/config/gdrive/callback", false},
		{"http://example.com/api/config/gdrive/callback", false},
		{"https://example.com/api/config/gdrive/callback?x=1", false},
	} {
		t.Run(tc.redirect, func(t *testing.T) {
			cfg := GDriveWrapper{ClientID: "id", ClientSecret: "secret", RedirectURL: tc.redirect}
			err := validateDriveConfig(&cfg)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestDriveOAuthStateAndCallback(t *testing.T) {
	s := driveSettingsServer(t)
	u, err := buildDriveUploader(&GDriveWrapper{WatchDir: t.TempDir(), ClientID: "test-client", ClientSecret: "test-secret", UploadInterval: "1s", RedirectURL: "http://localhost:3400/api/config/gdrive/callback"})
	if err != nil {
		t.Fatal(err)
	}
	s.gdriveUploader = u
	exchangeCount := 0
	client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"user":{"emailAddress":"tester@example.com"}}`
		if r.URL.Host == "oauth.example.com" {
			exchangeCount++
			body = `{"access_token":"mock-token","refresh_token":"mock-refresh","token_type":"Bearer","expires_in":3600}`
			_ = r.ParseForm()
			if r.Form.Get("redirect_uri") != "http://localhost:3400/api/config/gdrive/callback" {
				t.Error("token exchange used different redirect")
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	u.ctx = context.WithValue(u.ctx, oauth2.HTTPClient, client)
	u.oauthCfg.Endpoint.TokenURL = "https://oauth.example.com/token"
	w := httptest.NewRecorder()
	s.handleGDriveAuthURL(w, httptest.NewRequest("GET", "/api/config/gdrive/auth-url", nil))
	var result struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	bridge, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	state := bridge.Query().Get("state")
	auth, err := url.Parse(bridge.Query().Get("authorization"))
	if err != nil {
		t.Fatal(err)
	}
	if bridge.Scheme != "http" || bridge.Host != "localhost:3400" || bridge.Path != "/drive-connect" || state == "" || state == "state-token" || auth.Query().Get("state") != state || auth.Query().Get("redirect_uri") != "http://localhost:3400/api/config/gdrive/callback" {
		t.Fatal("invalid authorization URL")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly {
		t.Fatal("missing state cookie")
	}
	callback := func(state string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/config/gdrive/callback?code=mock-code&state="+state, nil)
		if cookie {
			r.AddCookie(cookies[0])
		}
		w := httptest.NewRecorder()
		s.handleGDriveCallback(w, r)
		return w
	}
	if w := callback("wrong-state", true); w.Code != 400 {
		t.Fatal("invalid state accepted")
	}
	if w := callback(state, false); w.Code != 400 {
		t.Fatal("missing cookie accepted")
	}
	if exchangeCount != 0 {
		t.Fatal("invalid callback exchanged a code")
	}
	if w := callback(state, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Connected!") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !u.IsAuthorized() || u.ConnectedEmail() != "tester@example.com" || exchangeCount != 1 {
		t.Fatal("valid callback failed")
	}
	if w := callback(state, true); w.Code != 400 {
		t.Fatal("callback replay accepted")
	}
	if err := u.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if u.IsAuthorized() {
		t.Fatal("disconnect retained authorization")
	}
	if _, err := os.Stat(u.config.TokenPath); !os.IsNotExist(err) {
		t.Fatal("disconnect retained token")
	}
}

func TestDriveWatcherUploadsAndDeletesOnlyOnSuccess(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "results.txt")
			_ = os.WriteFile(file, []byte("result\n"), 0600)
			// A non-hidden custom token must never be uploaded.
			token := filepath.Join(dir, "private-token.json")
			_ = os.WriteFile(token, []byte("private"), 0600)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			uploads := 0
			client := &http.Client{Transport: driveTestTransport(func(r *http.Request) (*http.Response, error) {
				status := 200
				body := `{"files":[]}`
				if r.Method == "GET" && !strings.Contains(r.URL.Query().Get("q"), "'root' in parents") {
					t.Error("unscoped Drive search")
				}
				if r.Method != "GET" {
					uploads++
					body = `{"id":"uploaded"}`
					if !success {
						status = 503
						cancel()
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			service, err := drive.NewService(ctx, option.WithHTTPClient(client))
			if err != nil {
				t.Fatal(err)
			}
			u := &GDriveUploader{config: &GDriveConfig{WatchDir: dir, TokenPath: token, StateFilePath: filepath.Join(dir, ".state.json"), DeleteAfterUpload: true}, ctx: ctx, service: service, uploaded: make(map[string]bool)}
			u.scanAndUpload()
			_, err = os.Stat(file)
			if success && !os.IsNotExist(err) {
				t.Fatal("successful upload not removed")
			}
			if !success && err != nil {
				t.Fatal("failed upload removed local file")
			}
			if uploads != 1 {
				t.Fatalf("expected one result upload, got %d", uploads)
			}
			if _, err := os.Stat(token); err != nil {
				t.Fatal("credential file removed")
			}
		})
	}
}

func TestDriveOAuthStateExpiry(t *testing.T) {
	u := &GDriveUploader{states: map[string]time.Time{"expired": time.Now().Add(-time.Minute)}}
	if u.ConsumeState("expired") || u.ConsumeState("missing") {
		t.Fatal("expired or missing state accepted")
	}
}
