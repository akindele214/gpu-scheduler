import { useEffect, useState } from "react";
import { fetchConfig } from "../hooks/useApi";
import type { ConfigResponse } from "../types/api";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Badge } from "./ui/badge";

export default function ConfigSection() {
  const [config, setConfig] = useState<ConfigResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    fetchConfig()
      .then(setConfig)
      .catch((e) => setError(e.message));
  }, []);

  if (error) return <div className="text-destructive p-4">Error: {error}</div>;

  return (
    <div className="space-y-2">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-2 text-xl font-semibold"
      >
        <span>{open ? "▼" : "▶"}</span>
        Config
      </button>

      {open && config && (
        <Card>
          <CardContent className="pt-6">
            <div className="grid grid-cols-2 gap-6">
              {/* Scheduler */}
              <div className="space-y-2">
                <CardHeader className="p-0">
                  <CardTitle className="text-sm">Scheduler</CardTitle>
                </CardHeader>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Name</span>
                  <span>{config.scheduler.name}</span>
                  <span className="text-muted-foreground">Mode</span>
                  <span>{config.scheduler.mode}</span>
                  <span className="text-muted-foreground">Port</span>
                  <span>{config.scheduler.port}</span>
                  <span className="text-muted-foreground">Preemption</span>
                  <span>
                    {config.scheduler.preemption_enabled ? (
                      <Badge variant="secondary" className="text-green-600">Enabled</Badge>
                    ) : (
                      <Badge variant="secondary">Disabled</Badge>
                    )}
                  </span>
                  <span className="text-muted-foreground">Gang Timeout</span>
                  <span>{config.scheduler.gang_timeout_seconds}s</span>
                  <span className="text-muted-foreground">Checkpoint Timeout</span>
                  <span>{config.scheduler.checkpoint_timeout_seconds}s</span>
                </div>
              </div>

              {/* Queue */}
              <div className="space-y-2">
                <CardHeader className="p-0">
                  <CardTitle className="text-sm">Queue</CardTitle>
                </CardHeader>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Max Size</span>
                  <span>{config.queue.max_size}</span>
                  <span className="text-muted-foreground">Policy</span>
                  <span>{config.queue.default_policy}</span>
                </div>
              </div>

              {/* GPU */}
              <div className="space-y-2">
                <CardHeader className="p-0">
                  <CardTitle className="text-sm">GPU</CardTitle>
                </CardHeader>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Poll Interval</span>
                  <span>{config.gpu.poll_interval_seconds}s</span>
                  <span className="text-muted-foreground">Mock Mode</span>
                  <span>{config.gpu.mock_mode ? "Yes" : "No"}</span>
                </div>
              </div>

              {/* Workflows */}
              <div className="space-y-2">
                <CardHeader className="p-0">
                  <CardTitle className="text-sm">Workflows</CardTitle>
                </CardHeader>
                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                  <span className="text-muted-foreground">Enabled</span>
                  <span>{config.workflows.enabled ? "Yes" : "No"}</span>
                  <span className="text-muted-foreground">Default Priority</span>
                  <span>{config.workflows.default_priority}</span>
                </div>
                {config.workflows.types && config.workflows.types.length > 0 && (
                  <div className="flex gap-2 pt-1">
                    {config.workflows.types.map((wf) => (
                      <Badge key={wf.name} variant="outline">
                        {wf.name} (p:{wf.priority})
                      </Badge>
                    ))}
                  </div>
                )}
              </div>
            </div>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
