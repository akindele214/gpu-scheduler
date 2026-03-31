export type WorkflowType = 'training' | 'inference' | 'build';

export type MemoryMode = 'per-gpu' | 'total' | 'none';

export interface ClusterResponse {
  node_response: NodeResponse[];
  cluster_summary: ClusterSummary;
}

export interface ClusterSummary {
  total_nodes: number;
  total_gpus: number;
  healthy_gpus: number;
  total_memory_mb: number;
  reserved_memory_mb: number;
  avg_utilization: number;
  mps_gpus: number;
  non_mps_gpus: number;
}

export interface NodeResponse {
  node_name: string;
  gpus: GPU[];
  reported_at: string;
}

export interface MIGInstance {
  gu_index: number;
  ci_index: number;
  uuid: string;
  profile_name: string;
  profile_id: number;
  memory_mb: number;
  sm_count: number;
  placement_start: number;
  placement_size: number;
  is_available: boolean;
}

export interface GPU {
  uuid: string;
  index: number;
  name: string;
  total_memory_mb: number;
  used_memory_mb: number;
  free_memory_mb: number;
  utilization_gpu: number;
  temperature_c: number;
  is_healthy: boolean;
  mps_enabled: boolean;
  mig_enabled: boolean;
  mig_instances: MIGInstance[];
}

export interface CreatePodRequest {
  name: string;
  namespace: string;
  container_name: string;
  image: string;
  command: string[];
  memory_mb: number;
  gpu_count: number;
  workflow: WorkflowType;
  priority: number;
  shared: boolean;
  preemptible: boolean;
  gang_id: string;
  gang_size: number;
  check_point_cmd: string;
  restart_policy: string;
  memory_mode: MemoryMode;
  auto_resume: boolean;
  resume_cmd: string;
}

export interface CreatePodResponse {
  status: string;
  pod: string;
  namespace: string;
}

export interface PodResponse {
  name: string;
  namespace: string;
  phase: string; // Pending | Running | Succeeded | Failed
  node_name: string;
  created_at: string;
  memory_mb: number;
  gpu_count: number;
  workflow: string;
  priority: number;
  shared: boolean;
  preemptible: boolean;
  gang_id: string;
  assigned_gpus: string[];
}

export interface PodListResponse {
  pods: PodResponse[];
  total: number;
  pending_count: number;
  running_count: number;
  completed_count: number;
  failed_count: number;
}

export interface LogEntry {
  timestamp: string;
  category: string;
  message: string;
  raw: string;
}

export interface LogResponse {
  entries: LogEntry[];
  total: number;
}

export type SSEEventType =
  | 'pod-scheduled'
  | 'preemption'
  | 'gpu-report'
  | 'pod-completed'
  | 'pod-deleted'
  | 'pod-preempted'
  | 'pod-resumed'
  | 'scheduler-log';

export interface SSEEvent<T = unknown> {
  sse_event_type: SSEEventType;
  data: T;
}

export interface ConfigResponse {
  scheduler: SchedulerConfig;
  queue: QueueConfig;
  workflows: WorkflowConfig;
  gpu: GPUConfig;
  kubernetes: KubernetesConfig;
  logging: LoggingConfig;
}
export interface SchedulerConfig {
  name: string;
  port: number;
  metrics_port: number;
  mode: string;
  gang_timeout_seconds: number;
  preemption_enabled: boolean;
  checkpoint_timeout_seconds: number;
  preemption_grace_period: number;
}
export interface WorkFlowTypeConfig {
  name: string;
  priority: number;
  preemptible: boolean;
}
export interface WorkflowConfig {
  enabled: boolean;
  allow_custom: boolean;
  default_priority: number;
  types: WorkFlowTypeConfig[];
}
export interface QueueConfig {
  max_size: number;
  default_policy: string;
}
export interface GPUConfig {
  poll_interval_seconds: number;
  mock_mode: boolean;
}
export interface KubernetesConfig {
  kubeconfig: string;
  namespace: string;
}
export interface LoggingConfig {
  level: string;
  format: string;
}
