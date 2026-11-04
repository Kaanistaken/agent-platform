"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, fmtTime, RunSummary } from "@/lib/api";

export default function RunsPage() {
  const [runs, setRuns] = useState<RunSummary[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    api<RunSummary[]>("/runs").then(setRuns).catch((e) => setError(e.message));
  }, []);
  return (
    <>
      <h1>Runs of your team</h1>
      {error && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr><th>#</th><th>Agent</th><th>Task</th><th>Started by</th><th>Status</th><th>Created</th></tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id}>
              <td><Link href={`/runs/${r.id}`}>#{r.id}</Link></td>
              <td>{r.agent.name}{r.agent.deleted && " (deleted)"}</td>
              <td>{r.task.length > 80 ? r.task.slice(0, 80) + "…" : r.task}</td>
              <td>{r.started_by.username}</td>
              <td><span className={`badge ${r.status}`}>{r.status}</span> {r.error_code && <span className="muted">{r.error_code}</span>}</td>
              <td className="muted">{fmtTime(r.created_at)}</td>
            </tr>
          ))}
          {runs.length === 0 && <tr><td colSpan={6} className="muted">No runs yet.</td></tr>}
        </tbody>
      </table>
    </>
  );
}
