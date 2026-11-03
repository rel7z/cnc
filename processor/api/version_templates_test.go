package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/cnc-processor/internal/store"
	"cnc-enum/version"
)

func TestVersionTemplateCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "processor.db")
	db, err := store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 32)
	handler := (&Server{Store: db}).Handler(token)
	request := func(method, path string, body any, auth bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	checks := []map[string]any{
		{"id": "range", "name": "", "target": "wordpress:plugin", "identifier": "elementor", "group": "", "clauses": []map[string]string{{"operator": ">=", "version": "3.24"}, {"operator": "<=", "version": "3.25"}}},
		{"id": "custom", "name": "Joomla custom", "target": "joomla:plugin", "identifier": "example", "group": "system", "clauses": []map[string]string{{"operator": ">=", "version": "1"}, {"operator": "<", "version": "2"}}},
	}
	body := map[string]any{"name": " Plugins ", "checks": checks}
	if w := request("POST", "/v1/version-check-templates", body, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/v1/version-check-templates", body, true)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved store.VersionTemplate
	json.Unmarshal(w.Body.Bytes(), &saved)
	if saved.Name != "Plugins" || saved.Revision != 1 || saved.Checks[0].Name != "" || saved.Checks[0].Clauses[0].Version != "3.24" || saved.Checks[1].Group != "system" {
		t.Fatalf("draft lost: %+v", saved)
	}
	if w = request("POST", "/v1/version-check-templates", map[string]any{"name": "plugins", "checks": checks}, true); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	url := "/v1/version-check-templates/" + saved.ID
	body["revision"] = 1
	body["name"] = "Renamed"
	w = request("PUT", url, body, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var updated store.VersionTemplate
	json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Revision != 2 || updated.CreatedAt != saved.CreatedAt || updated.UpdatedAt == saved.UpdatedAt {
		t.Fatalf("revision/timestamps %+v", updated)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if w = request(method, url, body, true); w.Code != 409 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	db.Close()
	db, err = store.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.InitSchema(); err != nil {
		t.Fatal(err)
	}
	handler = (&Server{Store: db}).Handler(token)
	w = request("GET", url, nil, true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var reopened store.VersionTemplate
	json.Unmarshal(w.Body.Bytes(), &reopened)
	if reopened.Name != "Renamed" || reopened.Revision != 2 || len(reopened.Checks) != 2 {
		t.Fatal(reopened)
	}
	if w = request("GET", "/v1/version-check-templates", nil, true); w.Code != 200 || !strings.Contains(w.Body.String(), "Renamed") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("DELETE", url, map[string]int{"revision": 2}, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		body["revision"] = 2
		if w = request(method, url, body, true); w.Code != 404 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	list, _ := db.ListVersionTemplates(context.Background())
	if len(list) != 1 || !list[0].IsDefault || len(list[0].Checks) != 105 {
		t.Fatalf("expected only the persisted default template, got %d entries", len(list))
	}
}

func TestVersionTemplateMoreThanOneHundredChecks(t *testing.T) {
	db, err := store.NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()
	if err := db.InitSchema(); err != nil { t.Fatal(err) }
	checks := make([]store.TemplateCheck, 105)
	for i := range checks {
		checks[i] = store.TemplateCheck{ID: fmt.Sprintf("rule-%d", i), Target: "wordpress:plugin", Identifier: fmt.Sprintf("owned-%d", i), Clauses: []version.VersionClause{{Operator: "<", Version: "3.1.8.10"}}}
	}
	created, err := db.CreateVersionTemplate(context.Background(), "Large", checks)
	if err != nil { t.Fatal(err) }
	if len(created.Checks) != 105 { t.Fatalf("lost checks: %d", len(created.Checks)) }
	loaded, err := db.GetVersionTemplate(context.Background(), created.ID)
	if err != nil || len(loaded.Checks) != 105 { t.Fatalf("round-trip: %v, %d", err, len(loaded.Checks)) }
}

func TestVersionTemplateValidation(t *testing.T) {
	db, err := store.NewStore(filepath.Join(t.TempDir(), "processor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.InitSchema()
	token := strings.Repeat("t", 32)
	handler := (&Server{Store: db}).Handler(token)
	for _, body := range []string{
		`{"name":"","checks":[]}`, `{"name":"Empty","checks":[]}`,
		`{"name":"Bad","checks":[{"id":"1","target":"wordpress:plugin","identifier":"","clauses":[{"operator":"=","version":"1"}]}]}`,
		`{"name":"Bad","checks":[{"id":"1","target":"joomla:plugin","identifier":"example","group":"","clauses":[{"operator":"=","version":"1"}]}]}`,
		`{"name":"Bad","checks":[{"id":"1","target":"wordpress:core","clauses":[{"operator":"=","version":"1.*"}]}]}`,
		`{"name":"Bad","checks":[{"id":"1","target":"wordpress:core","clauses":[{"operator":">","version":"2"},{"operator":"<=","version":"1"}]}]}`,
		`{"name":"Duplicate","checks":[{"id":"1","target":"wordpress:core","clauses":[{"operator":"=","version":"1"}]},{"id":"2","target":"wordpress:core","clauses":[{"operator":"=","version":"1.0.0"}]}]}`,
		`{"name":"Bad type","checks":[{"id":"1","target":"joomla:plugin:system","identifier":"example","clauses":[{"operator":"=","version":"1"}]}]}`,
		`{"name":"Unknown","checks":[],"scope":{}}`, `{} {}`,
	} {
		r := httptest.NewRequest("POST", "/v1/version-check-templates", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("body=%s code=%d response=%s", body, w.Code, w.Body.String())
		}
	}
}
