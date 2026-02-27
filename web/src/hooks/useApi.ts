import type {
  ClusterResponse,
  ConfigResponse,
  CreatePodRequest,
  CreatePodResponse,
  PodListResponse,
  SSEEvent,
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
  es.onmessage = (e) => onEvent(JSON.parse(e.data));
  return es; // caller can call es.close() to disconnect
}
