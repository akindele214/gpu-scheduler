import { useSSE } from "@/hooks/useSSE";
import { useCallback, useEffect, useRef, useState } from "react";
import { createPod, deletePod, fetchPodLogs, fetchPods, streamPodLogs } from "../hooks/useApi";
import type {
  CreatePodRequest,
  PodListResponse,
  PodResponse,
} from "../types/api";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

function phaseBadge(phase: string) {
  switch (phase) {
    case "Running":
      return <Badge className="bg-green-600">Running</Badge>;
    case "Pending":
      return <Badge className="bg-yellow-600">Pending</Badge>;
    case "ContainerCreating":
      return <Badge className="bg-yellow-600">Container Creating</Badge>;
    case "PodInitializing":
      return <Badge className="bg-yellow-600">Pod Initializing</Badge>;
    case "Succeeded":
      return <Badge className="bg-blue-600">Succeeded</Badge>;
    case "CrashLoopBackOff":
      return <Badge variant="destructive">CrashLoopBackOff</Badge>;
    case "ErrImagePull":
      return <Badge variant="destructive">ErrImagePull</Badge>;
    case "ImagePullBackOff":
      return <Badge variant="destructive">Failed</Badge>;
    case "OOMKilled":
      return <Badge variant="destructive">OOM Killed</Badge>;
    case "Error":
      return <Badge variant="destructive">Error</Badge>;
    case "Failed":
      return <Badge variant="destructive">Failed</Badge>;
    default:
      return <Badge variant="secondary">{phase}</Badge>;
  }
}

const defaultForm: CreatePodRequest = {
  name: "",
  namespace: "default",
  container_name: "gpu-worker",
  image: "nvidia/cuda:12.2.0-runtime-ubuntu22.04",
  command: ["sleep", "300"],
  memory_mb: 4096,
  gpu_count: 1,
  workflow: "training",
  priority: 50,
  shared: false,
  preemptible: false,
  gang_id: "",
  gang_size: 0,
  check_point_cmd: "",
  restart_policy: "Never",
  memory_mode: "per-gpu",
};

function SubmitPodForm({ onCreated }: { onCreated: () => void }) {
  const [form, setForm] = useState<CreatePodRequest>({ ...defaultForm });
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const update = <K extends keyof CreatePodRequest>(
    key: K,
    value: CreatePodRequest[K]
  ) => setForm((prev) => ({ ...prev, [key]: value }));

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);
    try {
      await createPod(form);
      setForm({ ...defaultForm });
      onCreated();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create pod");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Submit Pod</CardTitle>
      </CardHeader>
      <CardContent>
        <form onSubmit={handleSubmit} className="space-y-4">
          {/* Row 1: Name, Namespace, Image */}
          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-1">
              <Label htmlFor="name">Pod Name</Label>
              <Input
                id="name"
                value={form.name}
                onChange={(e) => update("name", e.target.value)}
                placeholder="my-training-job"
                required
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="namespace">Namespace</Label>
              <Input
                id="namespace"
                value={form.namespace}
                onChange={(e) => update("namespace", e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="image">Image</Label>
              <Input
                id="image"
                value={form.image}
                onChange={(e) => update("image", e.target.value)}
              />
            </div>
          </div>

          {/* Row 2: Container Name, Command */}
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1">
              <Label htmlFor="container_name">Container Name</Label>
              <Input
                id="container_name"
                value={form.container_name}
                onChange={(e) => update("container_name", e.target.value)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="command">Command (comma-separated)</Label>
              <Input
                id="command"
                value={form.command.join(", ")}
                onChange={(e) =>
                  update(
                    "command",
                    e.target.value.split(",").map((s) => s.trim())
                  )
                }
                placeholder="sleep, 300"
              />
            </div>
          </div>

          {/* Row 3: Memory, GPU Count, Workflow, Priority */}
          <div className="grid grid-cols-4 gap-4">
            <div className="space-y-1">
              <Label htmlFor="memory_mb">Memory (MB)</Label>
              <Input
                id="memory_mb"
                type="number"
                value={form.memory_mb}
                onChange={(e) => update("memory_mb", parseInt(e.target.value) || 0)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="gpu_count">GPU Count</Label>
              <Input
                id="gpu_count"
                type="number"
                value={form.gpu_count}
                onChange={(e) => update("gpu_count", parseInt(e.target.value) || 1)}
              />
            </div>
            <div className="space-y-1">
              <Label>Workflow</Label>
              <Select
                value={form.workflow}
                onValueChange={(v) =>
                  update("workflow", v as CreatePodRequest["workflow"])
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="training">Training</SelectItem>
                  <SelectItem value="inference">Inference</SelectItem>
                  <SelectItem value="build">Build</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label htmlFor="priority">Priority</Label>
              <Input
                id="priority"
                type="number"
                value={form.priority}
                onChange={(e) => update("priority", parseInt(e.target.value) || 0)}
              />
            </div>
          </div>

          {/* Row 4: Toggles — Shared, Preemptible, Restart Policy, Memory Mode */}
          <div className="grid grid-cols-4 gap-4">
            <div className="flex items-center gap-2 pt-5">
              <input
                type="checkbox"
                id="shared"
                checked={form.shared}
                onChange={(e) => update("shared", e.target.checked)}
                className="h-4 w-4 rounded border"
              />
              <Label htmlFor="shared">Shared GPU</Label>
            </div>
            <div className="flex items-center gap-2 pt-5">
              <input
                type="checkbox"
                id="preemptible"
                checked={form.preemptible}
                onChange={(e) => update("preemptible", e.target.checked)}
                className="h-4 w-4 rounded border"
              />
              <Label htmlFor="preemptible">Preemptible</Label>
            </div>
            <div className="space-y-1">
              <Label>Restart Policy</Label>
              <Select
                value={form.restart_policy}
                onValueChange={(v) => update("restart_policy", v)}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="Never">Never</SelectItem>
                  <SelectItem value="Always">Always</SelectItem>
                  <SelectItem value="OnFailure">OnFailure</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label>Memory Mode</Label>
              <Select
                value={form.memory_mode}
                onValueChange={(v) =>
                  update("memory_mode", v as CreatePodRequest["memory_mode"])
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="per-gpu">Per GPU</SelectItem>
                  <SelectItem value="total">Total</SelectItem>
                  <SelectItem value="none">None</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          {/* Row 5: Gang scheduling + Checkpoint */}
          <div className="grid grid-cols-3 gap-4">
            <div className="space-y-1">
              <Label htmlFor="gang_id">Gang ID</Label>
              <Input
                id="gang_id"
                value={form.gang_id}
                onChange={(e) => update("gang_id", e.target.value)}
                placeholder="Optional"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="gang_size">Gang Size</Label>
              <Input
                id="gang_size"
                type="number"
                value={form.gang_size}
                onChange={(e) => update("gang_size", parseInt(e.target.value) || 0)}
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="check_point_cmd">Checkpoint Command</Label>
              <Input
                id="check_point_cmd"
                value={form.check_point_cmd}
                onChange={(e) => update("check_point_cmd", e.target.value)}
                placeholder="Optional"
              />
            </div>
          </div>

          {error && <p className="text-sm text-destructive">{error}</p>}

          <Button type="submit" disabled={submitting}>
            {submitting ? "Submitting..." : "Submit Pod"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}

function PodsTable({
  pods,
  onDelete,
  onViewLogs,
  deletingPod,
}: {
  pods: PodResponse[];
  onDelete: (namespace: string, name: string) => void;
  onViewLogs: (namespace: string, name: string) => void;
  deletingPod: string | null;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Namespace</TableHead>
          <TableHead>Phase</TableHead>
          <TableHead>Node</TableHead>
          <TableHead>Memory</TableHead>
          <TableHead>GPUs</TableHead>
          <TableHead>Workflow</TableHead>
          <TableHead>Priority</TableHead>
          <TableHead>Shared</TableHead>
          <TableHead>Created</TableHead>
          <TableHead></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {pods.length === 0 ? (
          <TableRow>
            <TableCell colSpan={11} className="text-center text-muted-foreground">
              No pods found
            </TableCell>
          </TableRow>
        ) : (
          pods.map((pod) => (
            <TableRow key={`${pod.namespace}/${pod.name}`}>
              <TableCell className="font-medium">{pod.name}</TableCell>
              <TableCell>{pod.namespace}</TableCell>
              <TableCell>{phaseBadge(pod.phase)}</TableCell>
              <TableCell>{pod.node_name || "—"}</TableCell>
              <TableCell>{pod.memory_mb} MB</TableCell>
              <TableCell>{pod.gpu_count}</TableCell>
              <TableCell>{pod.workflow || "—"}</TableCell>
              <TableCell>{pod.priority}</TableCell>
              <TableCell>{pod.shared ? "Yes" : "No"}</TableCell>
              <TableCell className="text-xs text-muted-foreground">
                {new Date(pod.created_at).toLocaleTimeString()}
              </TableCell>
              <TableCell className="flex gap-2">
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => onViewLogs(pod.namespace, pod.name)}
                >
                  Logs
                </Button>
                <Button
                  variant="destructive"
                  size="xs"
                  disabled={deletingPod === `${pod.namespace}/${pod.name}`}
                  onClick={() => onDelete(pod.namespace, pod.name)}
                >
                  {deletingPod === `${pod.namespace}/${pod.name}` ? "Deleting..." : "Delete"}
                </Button>
              </TableCell>
            </TableRow>
          ))
        )}
      </TableBody>
    </Table>
  );
}

export default function PodsSection() {
  const [data, setData] = useState<PodListResponse | null>(null);
  const [logsPod, setLogsPod] = useState<{ namespace: string; name: string } | null>(null);
  const [logs, setLogs] = useState("");
  const [streaming, setStreaming] = useState(false)
  const controllerRef = useRef<AbortController | null>(null)
  const logsEndRef = useRef<HTMLDivElement |null>(null)
  const [error, setError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [deletingPod, setDeletingPod] = useState<string | null>(null);


  const load = useCallback(() => {
    fetchPods().then(setData).catch((e) => setError(e.message));
  }, []);

  useEffect(() => { load(); }, []);

  useSSE(['pod-scheduled', 'pod-completed', 'pod-deleted', 'preemption'], load);
  useEffect(() => {
    // load();
    const interval = setInterval(load, 300_000);
    return () => clearInterval(interval);
  }, []);

  const handleDelete = async (namespace: string, name: string) => {
    const key = `${namespace}/${name}`;
    setDeletingPod(key);
    try {
      await deletePod(namespace, name);
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete pod");
    } finally {
      setDeletingPod(null);
    }
  };
  const handleViewLogs = async (namespace: string, name: string) => {
    handleStopStream()
    setLogsPod({name: name, namespace: namespace})
    setLogs("")
    const text = await fetchPodLogs(namespace, name, 100)
    setLogs(text)
  }

  const handleFollow =(namespace: string, name: string)=> {
    controllerRef.current?.abort()
    setStreaming(true)
    setLogsPod({name: name, namespace: namespace})
    const controller = streamPodLogs(namespace, name, 100, (chunk) => setLogs(prev => prev + chunk))
    controllerRef.current = controller
  }
  const handleStopStream = () => {
    controllerRef.current?.abort()
    setStreaming(false)
  }
  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  if (error) return <div className="text-destructive p-4">Error: {error}</div>;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold">Pods</h2>
        <div className="flex items-center gap-4">
          {data && (
            <div className="flex gap-3 text-sm text-muted-foreground">
              <span>Total: {data.total}</span>
              <span>Running: {data.running_count}</span>
              <span>Pending: {data.pending_count}</span>
              <span>Completed: {data.completed_count}</span>
              <span>Failed: {data.failed_count}</span>
            </div>
          )}
          <Button onClick={() => setShowForm(!showForm)}>
            {showForm ? "Cancel" : "+ Submit Pod"}
          </Button>
        </div>
      </div>

      {showForm && (
        <SubmitPodForm
          onCreated={() => {
            load();
            setShowForm(false);
          }}
        />
      )}

      <Card>
        <CardContent className="pt-4">
          {data ? (
            <PodsTable pods={data.pods} onDelete={handleDelete} onViewLogs={handleViewLogs} deletingPod={deletingPod} />
          ) : (
            <p className="p-4 text-muted-foreground">Loading pods...</p>
          )}
        </CardContent>
      </Card>

      {logsPod && (
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm font-medium">
                Logs: {logsPod.namespace}/{logsPod.name}
              </CardTitle>
              <div className="flex gap-2">
                {streaming ? (
                  <Button variant="outline" size="xs" onClick={handleStopStream}>
                    Stop
                  </Button>
                ) : (
                  <Button
                    variant="outline"
                    size="xs"
                    onClick={() => handleFollow(logsPod.namespace, logsPod.name)}
                  >
                    Follow
                  </Button>
                )}
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => handleViewLogs(logsPod.namespace, logsPod.name)}
                >
                  Refresh
                </Button>
                <Button
                  variant="outline"
                  size="xs"
                  onClick={() => { handleStopStream(); setLogsPod(null); setLogs(""); }}
                >
                  Close
                </Button>
              </div>
            </div>
          </CardHeader>
          <CardContent>
            <div className="max-h-96 overflow-auto rounded bg-muted p-3">
              <pre className="text-xs whitespace-pre-wrap break-all">
                {logs || "No logs available"}
                <div ref={logsEndRef} />
              </pre>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
