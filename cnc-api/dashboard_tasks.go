package cnc

import "sort"

// Bootstrap state must fit a forwarding tunnel. Historical job details remain
// available through /api/jobs/{id}/tasks; this does not remove stored tasks.
const dashboardTaskLimit = 100

// Caller holds s.mu for reading. Sorting a separate slice leaves storage intact.
func (s *Server) dashboardTasksLocked(limit int) []*Task {
	tasks := make([]*Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].ID < tasks[j].ID
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks
}
