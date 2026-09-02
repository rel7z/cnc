export interface Stats {
  workers_total: number;
  workers_online: number;
  jobs_total: number;
  jobs_running: number;
  tasks_total: number;
  tasks_pending: number;
  tasks_running: number;
  tasks_completed: number;
  tasks_failed: number;
}

export type WorkerStatus = "online" | "offline" | "busy";
export type TaskStatus =
  | "pending"
  | "assigned"
  | "running"
  | "completed"
  | "failed";
export type JobStatus =
  | "pending"
  | "running"
  | "completed"
  | "cancelled"
  | "failed";

export interface Worker {
  id: string;
  address: string;
  status: WorkerStatus;
  max_tasks: number;
  current_load: number;
  last_seen: string;
  registered: string;
  metadata?: Record<string, string>;
}

export interface Job {
  id: string;
  name: string;
  command: string;
  input_file: string;
  workers: number;       // number of parts / workers
  timeout_seconds: number;
  total_tasks: number;
  completed: number;
  failed: number;
  status: JobStatus;
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export interface TaskResult {
  message?: string;
}

export interface Task {
  id: string;
  job_id: string;
  type: string;
  status: TaskStatus;
  assigned_to?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  result?: TaskResult;
  error?: string;
  retry_count: number;
  priority: number;
}

export interface SSESnapshot {
  stats: Stats;
  workers: Worker[];
  jobs: Job[];
}

export interface SSEEvent {
  type:
    | "snapshot"
    | "worker_update"
    | "job_update"
    | "task_update"
    | "stats_update";
  payload: SSESnapshot | Worker | Job | Task | Stats;
}
