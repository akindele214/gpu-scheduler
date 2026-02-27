import { useEffect, useState } from "react";
import { fetchCluster } from "../hooks/useApi";
import type { ClusterResponse, GPU, NodeResponse } from "../types/api";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from "./ui/card";
import { Progress } from "./ui/progress";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";
import { Badge } from "./ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "./ui/table";

function timeAgo(dateStr: string): string {
  const seconds = Math.floor(
    (Date.now() - new Date(dateStr).getTime()) / 1000
  );
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  return `${Math.floor(seconds / 3600)}h ago`;
}

function memoryPercent(used: number, total: number): number {
  if (total === 0) return 0;
  return Math.round((used / total) * 100);
}

function GPUCard({ gpu }: { gpu: GPU }) {
  const memPct = memoryPercent(gpu.used_memory_mb, gpu.total_memory_mb);
  return (
    <div className="space-y-2 rounded-md border p-3">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">
          GPU {gpu.index}: {gpu.name}
        </span>
        <div className="flex items-center gap-2">
          {gpu.is_healthy ? (
            <Badge variant="secondary" className="text-green-600">
              Healthy
            </Badge>
          ) : (
            <Badge variant="destructive">Unhealthy</Badge>
          )}
          {gpu.mps_enabled && <Badge variant="outline" className="text-blue-500">MPS</Badge>}
          {gpu.mig_enabled && <Badge variant="outline">MIG</Badge>}
        </div>
      </div>

      <div className="space-y-1">
        <div className="flex justify-between text-xs text-muted-foreground">
          <span>Memory</span>
          <span>
            {(gpu.used_memory_mb / 1024).toFixed(1)}/
            {(gpu.total_memory_mb / 1024).toFixed(1)} GB ({memPct}%)
          </span>
        </div>
        <Progress value={memPct} />
      </div>

      <div className="space-y-1">
        <div className="flex justify-between text-xs text-muted-foreground">
          <span>Utilization</span>
          <span>{gpu.utilization_gpu}%</span>
        </div>
        <Progress value={gpu.utilization_gpu} />
      </div>

      <div className="flex gap-4 text-xs text-muted-foreground">
        <span>{gpu.temperature_c}°C</span>
        <span className="truncate text-[10px] opacity-60">{gpu.uuid}</span>
      </div>
    </div>
  );
}

function NodeCard({ node }: { node: NodeResponse }) {
  return (
    <Card className="min-w-[380px] flex-1">
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>{node.node_name}</CardTitle>
          <CardDescription>{timeAgo(node.reported_at)}</CardDescription>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {node.gpus.map((gpu) => (
          <GPUCard key={gpu.uuid} gpu={gpu} />
        ))}
      </CardContent>
    </Card>
  );
}

export default function ClusterOverview() {
  const [data, setData] = useState<ClusterResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const load = () => {
      fetchCluster()
        .then(setData)
        .catch((e) => setError(e.message));
    };
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, []);

  if (error) return <div className="text-destructive p-4">Error: {error}</div>;
  if (!data)
    return <div className="p-4 text-muted-foreground">Loading cluster data...</div>;

  const { cluster_summary: summary, node_response: nodes } = data;
  const memPct = memoryPercent(
    summary.reserved_memory_mb,
    summary.total_memory_mb
  );

  return (
    <div className="space-y-4">
      <h2 className="text-xl font-semibold">Cluster Overview</h2>

      {/* Summary */}
      <Card>
        <CardContent className="pt-6">
          <div className="mb-4 grid grid-cols-5 gap-6">
            <div>
              <p className="text-sm text-muted-foreground">Nodes</p>
              <p className="text-2xl font-bold">{summary.total_nodes}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">GPUs</p>
              <p className="text-2xl font-bold">{summary.total_gpus}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Healthy</p>
              <p className="text-2xl font-bold">{summary.healthy_gpus}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">MPS GPUs</p>
              <p className="text-2xl font-bold">{summary.mps_gpus}</p>
            </div>
            <div>
              <p className="text-sm text-muted-foreground">Non-MPS GPUs</p>
              <p className="text-2xl font-bold">{summary.non_mps_gpus}</p>
            </div>
          </div>

          <div className="space-y-3">
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>Avg Utilization</span>
                <span>{Math.round(summary.avg_utilization)}%</span>
              </div>
              <Progress value={summary.avg_utilization} />
            </div>
            <div className="space-y-1">
              <div className="flex justify-between text-sm">
                <span>Memory Reserved</span>
                <span>
                  {(summary.reserved_memory_mb / 1024).toFixed(1)}/
                  {(summary.total_memory_mb / 1024).toFixed(1)} GB ({memPct}%)
                </span>
              </div>
              <Progress value={memPct} />
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Nodes — Cards / Table toggle */}
      <Tabs defaultValue="cards">
        <TabsList>
          <TabsTrigger value="cards">Cards</TabsTrigger>
          <TabsTrigger value="table">Table</TabsTrigger>
        </TabsList>

        <TabsContent value="cards">
          <div className="flex gap-4 overflow-x-auto pb-2">
            {nodes.map((node) => (
              <NodeCard key={node.node_name} node={node} />
            ))}
          </div>
        </TabsContent>

        <TabsContent value="table">
          <Card>
            <CardContent className="pt-4">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>GPU</TableHead>
                    <TableHead>Memory</TableHead>
                    <TableHead>Utilization</TableHead>
                    <TableHead>Temp</TableHead>
                    <TableHead>Health</TableHead>
                    <TableHead>MPS</TableHead>
                    <TableHead>MIG</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {nodes.flatMap((node) =>
                    node.gpus.map((gpu) => (
                      <TableRow key={`${node.node_name}-${gpu.uuid}`}>
                        <TableCell className="font-medium">
                          {node.node_name}
                        </TableCell>
                        <TableCell>
                          {gpu.name}:{gpu.index}
                        </TableCell>
                        <TableCell>
                          {(gpu.used_memory_mb / 1024).toFixed(1)}/
                          {(gpu.total_memory_mb / 1024).toFixed(1)} GB
                        </TableCell>
                        <TableCell>{gpu.utilization_gpu}%</TableCell>
                        <TableCell>{gpu.temperature_c}°C</TableCell>
                        <TableCell>
                          {gpu.is_healthy ? (
                            <Badge
                              variant="secondary"
                              className="text-green-600"
                            >
                              Healthy
                            </Badge>
                          ) : (
                            <Badge variant="destructive">Unhealthy</Badge>
                          )}
                        </TableCell>
                        <TableCell>
                          {gpu.mps_enabled ? (
                            <Badge variant="outline" className="text-blue-500">On</Badge>
                          ) : (
                            "—"
                          )}
                        </TableCell>
                        <TableCell>
                          {gpu.mig_enabled ? (
                            <Badge variant="outline">On</Badge>
                          ) : (
                            "—"
                          )}
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  );
}
