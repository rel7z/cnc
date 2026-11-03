package cnc

import "github.com/user/cnc-processor/cluster"

func (w *WorkerAgent) taskExecutionResources(task *Task) cluster.Resources {
	if task.ExecutionResources != nil {
		return *task.ExecutionResources
	}
	r := cluster.Program{}.Resources()
	snapshot := cluster.SampleResources()
	// Non-Linux development hosts lack /proc. Production Linux assignments
	// carry the controller's reservation and use the enforced shared pool.
	if snapshot.TotalMemoryMB > 0 {
		r.MemoryBurst = true
		r.MemoryMB = cluster.JobMemoryBudgetMB(snapshot.TotalMemoryMB)
		available := snapshot.AvailableMemoryMB - cluster.WorkerMemoryReserveMB(snapshot.TotalMemoryMB)
		if available < r.MemoryMB {
			r.MemoryMB = available
		}
	}
	return r
}

// Must be called while the server mutex is held.
func (s *Server) ordinaryResources(w *Worker) (cluster.Resources, string) {
	reserved, _ := s.processorReservations(w.ID)
	return cluster.AutomaticResources(cluster.Worker{MaxTasks: w.MaxTasks, CurrentLoad: s.workerLoad(w), Resources: w.Resources}, reserved)
}
