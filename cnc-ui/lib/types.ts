import type { NetworkSnapshot } from "@/components/dashboard/WorkerNetwork";
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
  gdrive_enabled?: boolean;
  gdrive_uploaded?: number;
  gdrive_pending?: number;
  gdrive_last_upload?: string;
}

export type WorkerStatus = "online" | "offline" | "busy";
export type TaskStatus =
  | "pending"
  | "assigned"
  | "running"
  | "completed"
  | "completed_with_errors"
  | "cancelled"
  | "failed";
export type JobMode = "spread" | "broadcast" | "server";
export type JobStatus =
  | "pending"
  | "running"
  | "completed"
  | "completed_with_errors"
  | "cancelled"
  | "failed";

export interface Worker {
  resources?: { network?: NetworkSnapshot; cpu_percent: number; cpu_cores: number; available_memory_mb: number; total_memory_mb: number };
  enum_requests?: Record<string, { active: number; queued: number; cancelled: number }>;
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
  error?: string;
  reverse_ip_input?: { input_lines: number; domains: number; duplicate_inputs: number; invalid_inputs: number; dns_failures: number; duplicate_ips: number; skipped_ipv6: number; unique_ips: number; warnings?: string[] };
  enum?: { actor: string; artifact_sha256: string; acknowledged_at: string };
  id: string;
  name: string;
  command: string;
  mode: JobMode;
  input_file: string;
  workers: number;
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
  stdout?: string;
  stderr?: string;
  exit_code: number;
}

// Payload fields the server embeds inside each Task.
export interface TaskPayload {
  command: string;
  download_url: string;
  dest_name: string;   // original filename — saved to ~/<dest_name> on the worker
  chunk_path: string;  // server-side path, for display only
  timeout_seconds: number;
}

export interface Task {
  enum_publication?: string;
  id: string;
  job_id: string;
  type: string;
  payload?: Partial<TaskPayload>;
  status: TaskStatus;
  assigned_to?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  result?: TaskResult;
  error?: string;
  retry_count: number;
}

export interface SSESnapshot {
  stats: Stats;
  workers: Worker[];
  jobs: Job[];
  tasks: Task[];
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
