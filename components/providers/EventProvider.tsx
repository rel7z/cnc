"use client";

import React, {
  createContext,
  useContext,
  useEffect,
  useReducer,
  useRef,
} from "react";
import type { Job, Stats, Task, Worker } from "@/lib/types";

// ── State ────────────────────────────────────────────────────────────────────

export interface DashboardState {
  stats: Stats | null;
  workers: Record<string, Worker>;
  jobs: Record<string, Job>;
  tasks: Record<string, Task>;
  connected: boolean;
  lastUpdated: Date | null;
}

const initialState: DashboardState = {
  stats: null,
  workers: {},
  jobs: {},
  tasks: {},
  connected: false,
  lastUpdated: null,
};

// ── Reducer ──────────────────────────────────────────────────────────────────

type Action =
  | {
      type: "SNAPSHOT";
      payload: { stats: Stats; workers: Worker[]; jobs: Job[] };
    }
  | { type: "WORKER_UPDATE"; payload: Worker }
  | { type: "JOB_UPDATE"; payload: Job }
  | { type: "TASK_UPDATE"; payload: Task }
  | { type: "STATS_UPDATE"; payload: Stats }
  | { type: "SET_CONNECTED"; payload: boolean };

function reducer(state: DashboardState, action: Action): DashboardState {
  switch (action.type) {
    case "SNAPSHOT": {
      const workers: Record<string, Worker> = {};
      for (const w of action.payload.workers) workers[w.id] = w;
      const jobs: Record<string, Job> = {};
      for (const j of action.payload.jobs) jobs[j.id] = j;
      return {
        ...state,
        stats: action.payload.stats,
        workers,
        jobs,
        connected: true,
        lastUpdated: new Date(),
      };
    }
    case "WORKER_UPDATE":
      return {
        ...state,
        workers: { ...state.workers, [action.payload.id]: action.payload },
        lastUpdated: new Date(),
      };
    case "JOB_UPDATE":
      return {
        ...state,
        jobs: { ...state.jobs, [action.payload.id]: action.payload },
        lastUpdated: new Date(),
      };
    case "TASK_UPDATE":
      return {
        ...state,
        tasks: { ...state.tasks, [action.payload.id]: action.payload },
        lastUpdated: new Date(),
      };
    case "STATS_UPDATE":
      return { ...state, stats: action.payload, lastUpdated: new Date() };
    case "SET_CONNECTED":
      return { ...state, connected: action.payload };
    default:
      return state;
  }
}

// ── Context ──────────────────────────────────────────────────────────────────

interface DashboardContextValue extends DashboardState {
  dispatch: React.Dispatch<Action>;
}

const DashboardContext = createContext<DashboardContextValue>({
  ...initialState,
  dispatch: () => undefined,
});

export function useDashboard(): DashboardContextValue {
  return useContext(DashboardContext);
}

// ── Provider ─────────────────────────────────────────────────────────────────

interface EventProviderProps {
  children: React.ReactNode;
  initialData?: Partial<DashboardState>;
}

export function EventProvider({ children, initialData }: EventProviderProps) {
  const [state, dispatch] = useReducer(reducer, {
    ...initialState,
    ...initialData,
  });
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    function connect() {
      const es = new EventSource("/api/events");
      esRef.current = es;

      es.addEventListener("snapshot", (e: MessageEvent) => {
        try {
          const payload = JSON.parse(e.data) as {
            stats: Stats;
            workers: Worker[];
            jobs: Job[];
          };
          dispatch({ type: "SNAPSHOT", payload });
          dispatch({ type: "SET_CONNECTED", payload: true });
        } catch {
          // ignore malformed events
        }
      });

      es.addEventListener("worker_update", (e: MessageEvent) => {
        try {
          dispatch({ type: "WORKER_UPDATE", payload: JSON.parse(e.data) as Worker });
        } catch {
          // ignore
        }
      });

      es.addEventListener("job_update", (e: MessageEvent) => {
        try {
          dispatch({ type: "JOB_UPDATE", payload: JSON.parse(e.data) as Job });
        } catch {
          // ignore
        }
      });

      es.addEventListener("task_update", (e: MessageEvent) => {
        try {
          dispatch({ type: "TASK_UPDATE", payload: JSON.parse(e.data) as Task });
        } catch {
          // ignore
        }
      });

      es.addEventListener("stats_update", (e: MessageEvent) => {
        try {
          dispatch({ type: "STATS_UPDATE", payload: JSON.parse(e.data) as Stats });
        } catch {
          // ignore
        }
      });

      es.onerror = () => {
        dispatch({ type: "SET_CONNECTED", payload: false });
        es.close();
        // EventSource doesn't auto-reconnect after close; retry after 3s
        setTimeout(connect, 3000);
      };
    }

    connect();

    return () => {
      esRef.current?.close();
    };
  }, []);

  return (
    <DashboardContext.Provider value={{ ...state, dispatch }}>
      {children}
    </DashboardContext.Provider>
  );
}
