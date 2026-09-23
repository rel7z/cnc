import re

with open("cnc-api/server.go", "r") as f:
    content = f.read()

new_dispatch = """func (s *Server) dispatchTask(task *Task) {
	s.mu.Lock()

	if task.Status != TaskStatusPending {
		s.mu.Unlock()
		return
	}

	var chosen *Worker

	if task.AssignedTo != "" {
		// Broadcast task: pre-assigned to a specific worker.
		w, ok := s.workers[task.AssignedTo]
		if !ok || w.Status == WorkerStatusOffline || w.SendCh == nil {
			// Worker unavailable — requeue after a short delay.
			s.mu.Unlock()
			go func() {
				time.Sleep(200 * time.Millisecond)
				s.taskQueue <- task
			}()
			return
		}
		chosen = w
	} else {
		// Spread task: find least-loaded available worker.
		for _, w := range s.workers {
			if w.Status != WorkerStatusOnline || w.CurrentLoad >= w.MaxTasks || w.SendCh == nil {
				continue
			}
			if chosen == nil || w.CurrentLoad < chosen.CurrentLoad ||
				(w.CurrentLoad == chosen.CurrentLoad && w.ID < chosen.ID) {
				chosen = w
			}
		}

		if chosen == nil {
			s.mu.Unlock()
			go func() {
				time.Sleep(200 * time.Millisecond)
				s.taskQueue <- task
			}()
			return
		}
	}

	task.Status = TaskStatusAssigned
	task.AssignedTo = chosen.ID
	now := time.Now()
	task.StartedAt = &now
	chosen.CurrentLoad++
	if chosen.CurrentLoad >= chosen.MaxTasks {
		chosen.Status = WorkerStatusBusy
	}
	taskCopy := *task
	workerCopy := *chosen

	msg, err := NewMessage(MsgTypeAssignTask, AssignTaskPayload{Task: *task})
	if err != nil {
		log.Printf("Failed to build assign-task message: %v", err)
		s.requeueTask(task, chosen)
		taskCopy = *task
		workerCopy = *chosen
		s.mu.Unlock()
		
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
		go func() { s.taskQueue <- task }()
		return
	}

	select {
	case chosen.SendCh <- msg:
		log.Printf("Dispatched task %s → worker %s", task.ID, chosen.ID)
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
	default:
		log.Printf("Worker %s send buffer full, requeueing task %s", chosen.ID, task.ID)
		s.requeueTask(task, chosen)
		taskCopy = *task
		workerCopy = *chosen
		s.mu.Unlock()
		s.broadcast(SSEEvent{Type: SSEEventTask, Payload: taskCopy})
		s.broadcast(SSEEvent{Type: SSEEventWorker, Payload: workerCopy})
		s.broadcastStats()
		go func() { s.taskQueue <- task }()
	}
}

func (s *Server) requeueTask(task *Task, w *Worker) {
	task.Status = TaskStatusPending
	task.AssignedTo = ""
	if w != nil {
		w.CurrentLoad--
		if w.CurrentLoad < 0 {
			w.CurrentLoad = 0
		}
		if w.CurrentLoad < w.MaxTasks {
			w.Status = WorkerStatusOnline
		}
	}
}"""

# Find the block from func (s *Server) dispatchTask(task *Task) { to the end of requeueTask
start_idx = content.find("func (s *Server) dispatchTask(task *Task) {")
end_idx = content.find("func (s *Server) heartbeatChecker() {")

if start_idx != -1 and end_idx != -1:
    patched = content[:start_idx] + new_dispatch + "\n\n// ── Heartbeat checker ─────────────────────────────────────────────────────────\n\n" + content[end_idx:]
    with open("cnc-api/server.go", "w") as f:
        f.write(patched)
    print("Patched successfully!")
else:
    print("Could not find the bounds!")
