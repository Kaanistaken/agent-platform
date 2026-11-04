"use client";

import { useParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api, fmtTime } from "@/lib/api";

// Live run view. Everything shown comes from the run's event stream (SSE,
// D67): a new page replays all events, a reconnect resumes after the last
// event ID (EventSource sends Last-Event-ID itself).

type Ev = { seq: number; type: string; data: any };

type Call = {
  id: number;
  name: string;
  status: string;
  arguments?: any;
  raw_arguments?: string;
  request?: any;
  requires_approval?: boolean;
  approval_revision: number;
  approval_expires_at?: string;
  reason_code?: string;
  error?: string;
  note?: string;
  result?: string;
  http_status?: number;
};

type RunInfo = {
  id: number;
  task: string;
  status: string;
  agent: { name: string };
  agent_snapshot: { provider: string; model: string };
  started_by: { username: string };
  can_decide: boolean;
  can_cancel: boolean;
};

export default function RunPage() {
  const { id } = useParams<{ id: string }>();
  const [run, setRun] = useState<RunInfo | null>(null);
  const [events, setEvents] = useState<Ev[]>([]);
  const [connected, setConnected] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const seen = useRef(new Set<number>());

  useEffect(() => {
    api<RunInfo>(`/runs/${id}`).then(setRun).catch((e) => setError(e.message));
    const es = new EventSource(`/api/runs/${id}/events`);
    const types = ["run_status", "text_delta", "attempt_discarded", "model_retry", "tool_call", "tool_call_status",
      "tool_result", "usage", "run_finished"];
    for (const t of types) {
      es.addEventListener(t, (m) => {
        const msg = m as MessageEvent;
        const seq = Number(msg.lastEventId);
        if (seen.current.has(seq)) return;
        seen.current.add(seq);
        setEvents((prev) => [...prev, { seq, type: t, data: JSON.parse(msg.data) }]);
        if (t === "run_finished") {
          es.close(); // the server closes after the final event; do not reconnect
          setConnected(false);
        }
      });
    }
    es.onopen = () => setConnected(true);
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [id]);

  // Fold the events into the current state of the run.
  let status = run?.status ?? "";
  let finished: any = null;
  const calls = new Map<number, Call>();
  const discarded = new Set<number>();
  const usage: any[] = [];
  type Item = { kind: "text"; attempt: number; mc: number; text: string } | { kind: "call"; id: number } | { kind: "retry"; data: any };
  const items: Item[] = [];
  for (const e of events) {
    const d = e.data;
    switch (e.type) {
      case "run_status":
        status = d.status;
        break;
      case "text_delta": {
        const last = items[items.length - 1];
        if (last && last.kind === "text" && last.mc === d.model_call_id) last.text += d.text;
        else items.push({ kind: "text", attempt: d.attempt, mc: d.model_call_id, text: d.text });
        break;
      }
      case "attempt_discarded":
        discarded.add(d.model_call_id);
        break;
      case "model_retry":
        items.push({ kind: "retry", data: d });
        break;
      case "tool_call":
        calls.set(d.id, { ...d });
        items.push({ kind: "call", id: d.id });
        break;
      case "tool_call_status": {
        const c = calls.get(d.id);
        if (c) Object.assign(c, { status: d.status, reason_code: d.reason_code, note: d.note },
          d.approval_revision ? { approval_revision: d.approval_revision } : {});
        break;
      }
      case "tool_result": {
        const c = calls.get(d.id);
        if (c) Object.assign(c, { status: d.status, reason_code: d.reason_code, result: d.text, http_status: d.http_status });
        break;
      }
      case "usage":
        usage.push(d);
        break;
      case "run_finished":
        status = d.status;
        finished = d;
        break;
    }
  }
  const terminal = ["succeeded", "failed", "cancelled"].includes(status);

  async function decide(call: Call, kind: "approve" | "reject") {
    setError("");
    try {
      await api(`/runs/${id}/tool-calls/${call.id}/${kind}`, {
        method: "POST",
        body: kind === "approve" ? { approval_revision: call.approval_revision } : { approval_revision: call.approval_revision, note },
      });
      setNote("");
    } catch (e: any) {
      setError(e.message);
    }
  }

  async function cancel() {
    if (!window.confirm("Are you sure you want to cancel this run?")) return;
    try {
      await api(`/runs/${id}/cancel`, { method: "POST" });
    } catch (e: any) {
      setError(e.message);
    }
  }

  const tokens = usage.reduce((a, u) => ({ in: a.in + (u.input_tokens ?? 0), out: a.out + (u.output_tokens ?? 0) }), { in: 0, out: 0 });

  return (
    <>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h1>
          Run #{id} <span className={`badge ${status}`}>{status}</span>
        </h1>
        {run?.can_cancel && !terminal && <button className="danger" onClick={cancel}>Cancel run</button>}
      </div>
      {run && (
        <p className="muted">
          {run.agent.name} · {run.agent_snapshot.provider}/{run.agent_snapshot.model} · started by {run.started_by.username} ·{" "}
          {connected ? "live" : terminal ? "finished" : "reconnecting…"}
          {usage.length > 0 && ` · tokens in ${tokens.in} / out ${tokens.out}`}
        </p>
      )}
      {error && <div className="error">{error}</div>}
      <div className="panel">
        <strong>Task</strong>
        <pre>{run?.task}</pre>
      </div>

      {items.map((it, i) => {
        if (it.kind === "text") {
          return (
            <div key={i} className={`event ${discarded.has(it.mc) ? "discarded" : ""}`}>
              <div className="muted">model{discarded.has(it.mc) ? " (attempt discarded, retried)" : ""}</div>
              <div style={{ whiteSpace: "pre-wrap" }}>{it.text}</div>
            </div>
          );
        }
        if (it.kind === "retry") {
          return (
            <div key={i} className="event err muted">
              Model call failed (attempt {it.data.attempt}), retrying in {Number(it.data.wait_seconds).toFixed(1)} s: {it.data.reason}
            </div>
          );
        }
        const c = calls.get(it.id)!;
        const waiting = c.status === "pending_approval";
        return (
          <div key={i} className={`event tool ${waiting ? "approval" : ""}`}>
            <div className="row">
              <strong>Tool call: {c.name}</strong> <span className={`badge ${c.status}`}>{c.status}</span>
              {c.reason_code && <span className="muted">{c.reason_code}</span>}
            </div>
            {c.error && <div className="muted">Returned to the model: {c.error}</div>}
            {c.arguments && (
              <>
                <div className="muted">Parameters</div>
                <pre>{JSON.stringify(c.arguments, null, 2)}</pre>
              </>
            )}
            {c.request && (
              <>
                <div className="muted">Request (secrets are attached only when it is sent)</div>
                <pre>{JSON.stringify(c.request, null, 2)}</pre>
              </>
            )}
            {c.status === "invalidated" && (
              <div className="error">
                This approval was invalidated ({c.reason_code}). Restore the missing key or secret under Settings; the request then
                returns here for a new approval.
              </div>
            )}
            {waiting && run?.can_decide && (
              <div>
                <p className="muted">Approval expires {fmtTime(c.approval_expires_at)} · revision {c.approval_revision}</p>
                <input placeholder="Optional note for the model when rejecting" value={note} onChange={(e) => setNote(e.target.value)} />
                <div className="row" style={{ marginTop: 8 }}>
                  <button className="ok" onClick={() => decide(c, "approve")}>Approve</button>
                  <button className="danger" onClick={() => decide(c, "reject")}>Reject</button>
                </div>
              </div>
            )}
            {waiting && !run?.can_decide && <p className="muted">Waiting for the person who started the run to decide.</p>}
            {c.note && <p className="muted">Rejection note: {c.note}</p>}
            {c.result && (
              <>
                <div className="muted">Result</div>
                <pre>{c.result}</pre>
              </>
            )}
          </div>
        );
      })}

      {finished && (
        <div className={`event ${finished.status === "succeeded" ? "final" : "err"}`}>
          <strong>Run {finished.status}</strong>
          {finished.error_code && <span className="muted"> · {finished.error_code}</span>}
          {finished.error_message && <div className="error">{finished.error_message}</div>}
          {finished.final_output && <div style={{ whiteSpace: "pre-wrap", marginTop: 6 }}>{finished.final_output}</div>}
        </div>
      )}
    </>
  );
}
