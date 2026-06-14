import { useCallback, useEffect, useState } from "react";
import {
  fetchInferenceWorkers,
  fetchProxyPressure,
  fetchProxyWorkerStats,
} from "../hooks/useApi";
import type {
  InferenceWorker,
  PressureReport,
  PressureState,
  WorkerStat,
} from "../types/api";
import { Badge } from "./ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "./ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

function formatMs(value?: number): string {
  if (!value || value <= 0) return "no traffic yet";
  return `${Math.round(value)}ms`;
}

function formatInflight(value?: number): string {
  return `${value ?? 0}`;
}

function roleBadge(role: string) {
  switch (role) {
    case "prefill":
      return <Badge className="bg-sky-600">Prefill</Badge>;
    case "decode":
      return <Badge className="bg-amber-600">Decode</Badge>;
    case "unified":
      return <Badge className="bg-emerald-600">Unified</Badge>;
    default:
      return <Badge variant="secondary">{role || "unknown"}</Badge>;
  }
}

function stateBadge(state: string) {
  switch (state) {
    case "ready":
      return <Badge className="bg-green-600">Ready</Badge>;
    case "draining":
      return <Badge className="bg-orange-600">Draining</Badge>;
    case "starting":
      return <Badge className="bg-yellow-600">Starting</Badge>;
    case "removed":
      return <Badge variant="destructive">Removed</Badge>;
    default:
      return <Badge variant="secondary">{state || "unknown"}</Badge>;
  }
}

function pressureBadge(state?: PressureState) {
  switch (state) {
    case "prefill_hot":
      return <Badge className="bg-sky-600">Prefill hot</Badge>;
    case "decode_hot":
      return <Badge className="bg-amber-600">Decode hot</Badge>;
    case "normal":
      return <Badge className="bg-green-600">Normal</Badge>;
    default:
      return <Badge variant="secondary">No pressure data</Badge>;
  }
}

function boolBadge(value: boolean, label: string) {
  return value ? (
    <Badge variant="outline" className="text-green-600">
      {label}
    </Badge>
  ) : (
    <Badge variant="outline" className="text-muted-foreground">
      No
    </Badge>
  );
}

function groupKey(worker: InferenceWorker): string {
  return worker.model_group || "unassigned";
}

export default function InferenceSection() {
  const [workers, setWorkers] = useState<InferenceWorker[]>([]);
  const [stats, setStats] = useState<WorkerStat[]>([]);
  const [pressure, setPressure] = useState<PressureReport[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [proxyError, setProxyError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [workersResult, statsResult, pressureResult] = await Promise.allSettled([
      fetchInferenceWorkers(),
      fetchProxyWorkerStats(),
      fetchProxyPressure(),
    ]);
    let nextError: string | null = null;
    let nextProxyError: string | null = null;

    if (workersResult.status === "fulfilled") {
      setWorkers(workersResult.value);
    } else {
      nextError = workersResult.reason.message;
    }

    if (statsResult.status === "fulfilled") {
      setStats(statsResult.value);
    } else {
      setStats([]);
      nextProxyError = statsResult.reason.message;
    }

    if (pressureResult.status === "fulfilled") {
      setPressure(pressureResult.value);
    } else {
      setPressure([]);
      nextProxyError = nextProxyError ?? pressureResult.reason.message;
    }

    setError(nextError);
    setProxyError(nextProxyError);
  }, []);

  useEffect(() => {
    const firstLoad = window.setTimeout(() => void load(), 0);
    const id = setInterval(() => void load(), 5_000);
    return () => {
      window.clearTimeout(firstLoad);
      clearInterval(id);
    };
  }, [load]);

  const statsByID = new Map(stats.map((stat) => [stat.id, stat]));
  const pressureByGroup = new Map(
    pressure.map((report) => [report.model_group, report])
  );
  const modelGroups = Array.from(new Set(workers.map(groupKey))).sort();

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-xl font-semibold">Inference Awareness</h2>
        <p className="text-sm text-muted-foreground">
          Scheduler worker discovery joined with proxy runtime stats.
        </p>
      </div>

      {error && <div className="text-destructive">Error: {error}</div>}
      {proxyError && (
        <div className="rounded-md border border-yellow-500/30 bg-yellow-500/10 p-3 text-sm text-yellow-700">
          Proxy stats unavailable: {proxyError}
        </div>
      )}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {modelGroups.map((modelGroup) => {
          const groupWorkers = workers.filter((worker) => groupKey(worker) === modelGroup);
          const prefillCount = groupWorkers.filter((worker) => worker.role === "prefill").length;
          const decodeCount = groupWorkers.filter((worker) => worker.role === "decode").length;
          const unifiedCount = groupWorkers.filter((worker) => worker.role === "unified").length;
          const routableCount = groupWorkers.filter((worker) => worker.routable).length;
          const report = pressureByGroup.get(modelGroup);

          return (
            <Card key={modelGroup}>
              <CardHeader>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <CardTitle className="text-base">{modelGroup}</CardTitle>
                    <CardDescription>{groupWorkers.length} worker(s)</CardDescription>
                  </div>
                  {pressureBadge(report?.pressure_state)}
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-3 text-sm">
                  <div>
                    <p className="text-muted-foreground">Unified</p>
                    <p className="text-2xl font-bold">{unifiedCount}</p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">Prefill</p>
                    <p className="text-2xl font-bold">{prefillCount}</p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">Decode</p>
                    <p className="text-2xl font-bold">{decodeCount}</p>
                  </div>
                </div>
                <div className="mt-4 grid grid-cols-3 gap-3 text-sm">
                  <div>
                    <p className="text-muted-foreground">Routable</p>
                    <p className="font-medium">
                      {routableCount}/{groupWorkers.length}
                    </p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">Inflight</p>
                    <p className="font-medium">{formatInflight(report?.inflight)}</p>
                  </div>
                  <div>
                    <p className="text-muted-foreground">TTFT / ITL</p>
                    <p className="font-medium">
                      {formatMs(report?.ttft_p95)} / {formatMs(report?.itl_p95)}
                    </p>
                  </div>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Inference Workers</CardTitle>
          <CardDescription>
            Runtime metrics are populated after traffic reaches the proxy.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Worker</TableHead>
                <TableHead>Model Group</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Routing</TableHead>
                <TableHead>Inflight</TableHead>
                <TableHead>TTFT p95</TableHead>
                <TableHead>ITL p95</TableHead>
                <TableHead>Endpoint</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {workers.map((worker) => {
                const stat = statsByID.get(worker.id);
                return (
                  <TableRow key={worker.id}>
                    <TableCell className="font-medium">{worker.id}</TableCell>
                    <TableCell>{worker.model_group || "unassigned"}</TableCell>
                    <TableCell>{roleBadge(worker.role)}</TableCell>
                    <TableCell>{stateBadge(worker.state)}</TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-2">
                        {boolBadge(worker.routable, "Routable")}
                        {worker.role === "prefill" || worker.role === "decode"
                          ? boolBadge(worker.gpu2gpu_ready, "GPU2GPU")
                          : null}
                      </div>
                    </TableCell>
                    <TableCell>{formatInflight(stat?.inflight)}</TableCell>
                    <TableCell>{formatMs(stat?.ttft_p95)}</TableCell>
                    <TableCell>{formatMs(stat?.itl_p95)}</TableCell>
                    <TableCell className="max-w-[260px] truncate text-xs text-muted-foreground">
                      {worker.endpoint || "missing endpoint"}
                    </TableCell>
                  </TableRow>
                );
              })}
              {workers.length === 0 && (
                <TableRow>
                  <TableCell colSpan={9} className="py-8 text-center text-muted-foreground">
                    No inference workers discovered yet.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
