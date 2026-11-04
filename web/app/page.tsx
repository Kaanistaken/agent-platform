"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Agent, api, fmtTime } from "@/lib/api";

type Approval = { id: number; run_id: number; name: string; status: string; reason_code: string; approval_expires_at: string };

export default function Home() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    api<Agent[]>("/agents").then(setAgents).catch((e) => setError(e.message));
    api<Approval[]>("/approvals").then(setApprovals).catch(() => {});
  }, []);

  return (
    <>
      {approvals.length > 0 && (
        <div className="panel" style={{ borderColor: "var(--warn)" }}>
          <strong>Waiting for your approval</strong>
          <ul>
            {approvals.map((a) => (
              <li key={a.id}>
                <Link href={`/runs/${a.run_id}`}>Run #{a.run_id}</Link>: <code>{a.name}</code>{" "}
                <span className={`badge ${a.status}`}>{a.status}</span>{" "}
                {a.reason_code && <span className="muted">({a.reason_code})</span>}{" "}
                <span className="muted">expires {fmtTime(a.approval_expires_at)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h1>Agents</h1>
        <Link href="/agents/new"><button className="primary">New agent</button></Link>
      </div>
      {error && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr><th>Name</th><th>Model</th><th>Tools</th><th>Owner</th></tr>
        </thead>
        <tbody>
          {agents.map((a) => (
            <tr key={a.id}>
              <td><Link href={`/agents/${a.id}`}>{a.name}</Link></td>
              <td>{a.provider} / {a.model}</td>
              <td>{a.tools.map((t) => t.name + (t.requires_approval ? " (approval)" : "")).join(", ")}</td>
              <td>{a.owner.username}</td>
            </tr>
          ))}
          {agents.length === 0 && (
            <tr><td colSpan={4} className="muted">No agents in your team yet.</td></tr>
          )}
        </tbody>
      </table>
    </>
  );
}
