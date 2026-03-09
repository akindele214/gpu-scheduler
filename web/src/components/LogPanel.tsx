import { useEffect, useRef } from "react";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";

interface LogPanelProps {
  title: string;
  logs: string;
  streaming: boolean;
  onFollow?: () => void;
  onStop?: () => void;
  onRefresh: () => void;
  onClose: () => void;
}

export default function LogPanel({
  title,
  logs,
  streaming,
  onFollow,
  onStop,
  onRefresh,
  onClose,
}: LogPanelProps) {
  const logsEndRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle className="text-sm font-medium">{title}</CardTitle>
          <div className="flex gap-2">
            {onFollow && onStop && (
              streaming ? (
                <Button variant="outline" size="xs" onClick={onStop}>
                  Stop
                </Button>
              ) : (
                <Button variant="outline" size="xs" onClick={onFollow}>
                  Follow
                </Button>
              )
            )}
            <Button variant="outline" size="xs" onClick={onRefresh}>
              Refresh
            </Button>
            <Button variant="outline" size="xs" onClick={onClose}>
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
  );
}
