import { useCallback, useEffect, useRef, useState } from "react";
import { fetchLogs, subscribeLogEvents } from "../hooks/useApi";
import type { LogEntry } from "../types/api";
import LogPanel from "./LogPanel";
import { Button } from "./ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";

const CATEGORIES = [
  "ALL",
  "SCHEDULE",
  "PREEMPT",
  "GANG",
  "REPORT",
  "CLEANUP",
  "AUTO-RESUME",
  "WARNING",
  "RECONCILE",
  "WEBHOOK",
  "GENERAL",
];

export default function LogViewer() {
  const [open, setOpen] = useState(false);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [category, setCategory] = useState("ALL");
  const [streaming, setStreaming] = useState(false);
  const esRef = useRef<EventSource | null>(null);

  const load = useCallback(() => {
    const cat = category === "ALL" ? undefined : category;
    fetchLogs(cat, 500).then((res) => setEntries(res.entries)).catch(console.error);
  }, [category]);

  useEffect(() => {
    if (open) load();
  }, [open, load]);

  const handleFollow = () => {
    esRef.current?.close();
    setStreaming(true);
    const es = subscribeLogEvents((entry) => {
      if (category === "ALL" || entry.category === category) {
        setEntries((prev) => [...prev.slice(-999), entry]);
      }
    });
    esRef.current = es;
  };

  const handleStop = () => {
    esRef.current?.close();
    esRef.current = null;
    setStreaming(false);
  };

  useEffect(() => {
    return () => { esRef.current?.close(); };
  }, []);

  if (!open) {
    return (
      <Button variant="outline" onClick={() => setOpen(true)}>
        Scheduler Logs
      </Button>
    );
  }

  const logsText = entries.map((e) => e.raw).join("\n");

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3">
        <h2 className="text-xl font-semibold">Scheduler Logs</h2>
        <Select value={category} onValueChange={(v) => { setCategory(v); }}>
          <SelectTrigger className="w-40">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {CATEGORIES.map((cat) => (
              <SelectItem key={cat} value={cat}>{cat}</SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <LogPanel
        title={`Scheduler Logs${category !== "ALL" ? ` [${category}]` : ""}`}
        logs={logsText}
        streaming={streaming}
        onFollow={handleFollow}
        onStop={handleStop}
        onRefresh={load}
        onClose={() => { handleStop(); setOpen(false); setEntries([]); }}
      />
    </div>
  );
}
