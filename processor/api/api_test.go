package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cnc-processor/internal/store"
)

func TestPausedQueueReportsPauseInsteadOfHistoricalMemoryBlock(t *testing.T) {
	ctx := context.Background()
	db, err := store.NewStore(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	_, ids, err := db.AcceptBatch(ctx, store.Batch{SourceID: "fixture", BatchID: "one", Filename: "fixture.txt", Generation: "g", PayloadFingerprint: "one", RecordsJSON: `["inert"]`}, []string{"fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.BlockRun(ctx, ids[0], "waiting for available memory"); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: db}
	for _, enabled := range []bool{false, true} {
		if err = db.SetEnabled(ctx, enabled); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		s.RunsHandler(response, httptest.NewRequest("GET", "/v1/runs", nil))
		var runs []store.Run
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &runs) != nil || len(runs) != 1 {
			t.Fatal(response.Body.String())
		}
		want := "processing is paused"
		if enabled {
			want = "waiting for available memory"
		}
		if runs[0].BlockedReason != want || runs[0].Status != "queued" {
			t.Fatalf("enabled=%v: %+v", enabled, runs[0])
		}
	}
}

func TestControlAuthenticationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "processor.db")
	db, err := store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	handler := (&Server{Store: db}).Handler(token)
	for _, tc := range []struct {
		body, token string
		status      int
	}{
		{`{"enabled":true}`, "", 401},
		{`{}`, token, 400},
		{`{"enabled":false} {}`, token, 400},
		{`{"enabled":true,"unexpected":1}`, token, 400},
		{`{"enabled":true}`, token, 200},
	} {
		r := httptest.NewRequest("PUT", "/v1/processing-state", strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("body=%s status=%d want=%d", tc.body, w.Code, tc.status)
		}
	}
	reopened, err := store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	enabled, err := reopened.IsEnabled(context.Background())
	if err != nil || !enabled {
		t.Fatalf("persisted enabled=%v err=%v", enabled, err)
	}
	r := httptest.NewRequest("GET", "/v1/health", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) || !strings.Contains(w.Body.String(), `"queued":0`) {
		t.Fatal(w.Body.String())
	}
}
