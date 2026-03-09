import type {
  ClusterResponse,
  ConfigResponse,
  CreatePodRequest,
  CreatePodResponse,
  LogEntry,
  LogResponse,
  PodListResponse,
  SSEEvent,
  SSEEventType,
} from '../types/api';

const API_BASE = '/api/v1/dashboard';

export async function fetchCluster(): Promise<ClusterResponse> {
  const res = await fetch(`${API_BASE}/cluster`);
  if (!res.ok) throw new Error(`Failed to fetch cluster: ${res.statusText}`);
  return res.json();
}

export async function fetchPods(): Promise<PodListResponse> {
  const res = await fetch(`${API_BASE}/pods`);
  if (!res.ok) throw new Error(`Failed to fetch pods: ${res.statusText}`);
  return res.json();
}

export async function fetchConfig(): Promise<ConfigResponse> {
  const res = await fetch(`${API_BASE}/config`);
  if (!res.ok) throw new Error(`Failed to fetch config: ${res.statusText}`);
  return res.json();
}

export async function createPod(
  req: CreatePodRequest,
): Promise<CreatePodResponse> {
  const res = await fetch(`${API_BASE}/pods`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  if (!res.ok) throw new Error(`Failed to create pod: ${res.statusText}`);
  return res.json();
}

export async function deletePod(
  namespace: string,
  name: string,
): Promise<void> {
  const res = await fetch(`${API_BASE}/pods/${namespace}/${name}`, {
    method: 'DELETE',
  });
  if (!res.ok) throw new Error(`Failed to delete pod: ${res.statusText}`);
}

export function subscribeEvents(
  onEvent: (event: SSEEvent) => void,
): EventSource {
  const es = new EventSource(`${API_BASE}/events`);
  const eventTypes = [
    'pod-scheduled',
    'preemption',
    'gpu-report',
    'pod-completed',
    'pod-deleted',
  ];
  for (const type of eventTypes) {
    es.addEventListener(type, (e) => {
      onEvent({
        sse_event_type: type as SSEEventType,
        data: JSON.parse((e as MessageEvent).data),
      });
    });
  }
  return es;
}

export async function fetchPodLogs(
  namespace: string,
  name: string,
  tail: number,
): Promise<string> {
  const url = `${API_BASE}/pods/${namespace}/${name}/logs?tail=${tail}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`Failed to fetch logs: ${res.statusText}`);
  return res.text();
}

export function streamPodLogs(
  namespace: string,
  name: string,
  tail: number,
  onChunk: (text: string) => void,
): AbortController {
  const url = `${API_BASE}/pods/${namespace}/${name}/logs?tail=${tail}&follow=true`;
  const controller = new AbortController();

  (async () => {
    const res = await fetch(url, { signal: controller.signal });
    if (!res.ok) throw new Error(`Failed to stream logs: ${res.statusText}`);

    const reader = res.body!.getReader();
    const decoder = new TextDecoder();

    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      onChunk(decoder.decode(value));
    }
  })().catch((err) => {
    if (err.name !== 'AbortError') console.error('streamPodLogs error:', err);
  });

  return controller;
}

export async function fetchLogs(category?: string, limit?: number): Promise<LogResponse> {
  const params = new URLSearchParams();
  if (category) params.set('category', category);
  if (limit) params.set('limit', limit.toString());
  const res = await fetch(`${API_BASE}/logs?${params}`);
  if (!res.ok) throw new Error(`Failed to fetch logs: ${res.statusText}`);
  return res.json();
}

export function subscribeLogEvents(onLog: (entry: LogEntry) => void): EventSource {
  const es = new EventSource(`${API_BASE}/events`);
  es.addEventListener('scheduler-log', (e) => {
    onLog(JSON.parse((e as MessageEvent).data));
  });
  return es;
}
