package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cnc-processor/internal/store"
)

func TestResetAPIRequiresAuthenticationConfirmationAndPause(t *testing.T) {
	s, err := store.NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InitSchema(); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	server := &Server{Store: s, ResetCheckpoints: s.ResetCaptureCheckpoints, ConfigurationStatus: func() (int, int) { return 0, 0 }}
	h := server.Handler(token)
	for _, tc := range []struct {
		body, auth string
		enabled    bool
		status     int
	}{
		{`{"confirm":"reset-checkpoints"}`, "", false, 401},
		{`{}`, token, false, 400},
		{`{"confirm":"reset-checkpoints","unknown":true}`, token, false, 400},
		{`{"confirm":"reset-checkpoints"}`, token, true, 409},
		{`{"confirm":"reset-checkpoints"}`, token, false, 200},
	} {
		s.SetEnabled(context.Background(), tc.enabled)
		r := httptest.NewRequest("POST", "/v1/checkpoints/reset", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
	}
	s.SetEnabled(context.Background(), true)
	w := httptest.NewRecorder()
	server.HealthHandler(w, httptest.NewRequest("GET", "/v1/health", nil))
	if !strings.Contains(w.Body.String(), "No programs configured") {
		t.Fatal("empty processor must explain why it is idle", w.Body.String())
	}
}
