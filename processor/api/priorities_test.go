package api

import (
	"context"
	"encoding/json"
	"github.com/user/cnc-processor/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPriorityCRUDValidationAuthenticationAndConflict(t *testing.T) {
	s, err := store.NewStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.InitSchema()
	s.RegisterProgram(context.Background(), "fixture", store.ProgramSpec{Version: "v", Limit: 1})
	token := strings.Repeat("x", 32)
	h := (&Server{Store: s}).Handler(token)
	call := func(method, path, body, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "/v1/program-priorities", "", "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("GET", "/v1/program-priorities", "", token); w.Code != 200 || !strings.Contains(w.Body.String(), `"effective_rank":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"rank":0,"revision":1}`, `{"rank":6,"revision":1}`, `{"rank":1.5,"revision":1}`, `{"rank":"5","revision":1}`, `{"revision":1}`, `{"rank":3,"revision":1,"extra":1}`, `{"rank":3,"revision":1} {}`} {
		if w := call("PUT", "/v1/program-priorities/fixture", body, token); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	w := call("PUT", "/v1/program-priorities/fixture", `{"rank":5,"revision":1}`, token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var p store.ProgramPriority
	json.Unmarshal(w.Body.Bytes(), &p)
	if p.Revision != 2 || p.EffectiveRank != 5 {
		t.Fatal(p)
	}
	if w = call("PUT", "/v1/program-priorities/fixture", `{"rank":2,"revision":1}`, token); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w = call("PUT", "/v1/program-priorities/missing", `{"rank":2,"revision":1}`, token); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w = call("PUT", "/v1/program-priorities/fixture", `{"rank":null,"revision":2}`, token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"effective_rank":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
