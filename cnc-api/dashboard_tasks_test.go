package cnc

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDashboardBootstrapBoundsHistoricalTasksWithoutDeletingThem(t *testing.T) {
	s := processorTestServer(t)
	base := time.Unix(1700000000, 0)
	for n := 0; n < 20000; n++ {
		id := fmt.Sprintf("history-%05d", n)
		s.tasks[id] = &Task{ID: id, JobID: "history", CreatedAt: base.Add(time.Duration(n) * time.Second), Status: TaskStatusCompleted}
	}
	snapshot := s.buildSnapshot().Payload.(SSESnapshot)
	if len(snapshot.Tasks) != dashboardTaskLimit || snapshot.Tasks[0].ID != "history-19999" {
		t.Fatal("snapshot is unbounded or excludes newest tasks")
	}
	if len(s.tasks) != 20000 {
		t.Fatal("historical storage was changed")
	}
	body, err := json.Marshal(snapshot)
	if err != nil || len(body) > 100000 {
		t.Fatalf("bootstrap grew unexpectedly: bytes=%d err=%v", len(body), err)
	}
	for _, tc := range []struct {
		query       string
		count, code int
	}{{"", 20000, 200}, {"?limit=100", 100, 200}, {"?limit=0", 0, 400}, {"?limit=1001", 0, 400}, {"?limit=bad", 0, 400}} {
		t.Run(tc.query, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.handleTasksAPI(w, httptest.NewRequest("GET", "/api/tasks"+tc.query, nil))
			if w.Code != tc.code {
				t.Fatalf("status=%d want %d", w.Code, tc.code)
			}
			if tc.code == 200 {
				var tasks []Task
				if json.Unmarshal(w.Body.Bytes(), &tasks) != nil || len(tasks) != tc.count {
					t.Fatal("wrong task count")
				}
			}
		})
	}
}

func TestDashboardTaskTieOrderIsStable(t *testing.T) {
	s := processorTestServer(t)
	for _, id := range []string{"z", "a", "m"} {
		s.tasks[id] = &Task{ID: id}
	}
	got := s.dashboardTasksLocked(2)
	if strings.Join([]string{got[0].ID, got[1].ID}, ",") != "a,m" {
		t.Fatal("unstable tie order")
	}
}
