"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import AgentForm from "@/components/AgentForm";
import { Agent, api, ApiError, User } from "@/lib/api";

export default function AgentPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [agent, setAgent] = useState<Agent | null>(null);
  const [me, setMe] = useState<User | null>(null);
  const [task, setTask] = useState("");
  const [editing, setEditing] = useState(false);
  const [missing, setMissing] = useState<number[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api<Agent>(`/agents/${id}`).then(setAgent).catch((e) => setError(e.message));
    api<User>("/me").then(setMe);
  }, [id]);

  async function start(confirm?: number[]) {
    setError("");
    try {
      const body: any = { task };
      if (confirm) body.confirm_missing_tool_ids = confirm;
      const res = await api<{ id: number }>(`/agents/${id}/runs`, { method: "POST", body });
      router.push(`/runs/${res.id}`);
    } catch (err: any) {
      // D40/D56: deleted tools must be confirmed before the run starts.
      if (err instanceof ApiError && err.status === 409 && err.body?.missing_tool_ids) {
        setMissing(err.body.missing_tool_ids);
      } else {
        setError(err.message);
      }
    }
  }

  async function action(path: string, method: string, confirmText?: string) {
    if (confirmText && !window.confirm(confirmText)) return;
    setError("");
    try {
      const res = await api<any>(path, { method });
      if (method === "DELETE") router.push("/");
      else if (res?.agent) router.push(`/agents/${res.agent.id}`);
    } catch (err: any) {
      setError(err.message);
    }
  }

  if (!agent) return error ? <div className="error">{error}</div> : <p className="muted">Loading…</p>;
  const isOwner = me?.id === agent.owner.id;

  return (
    <>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h1>
          {agent.name} {agent.deleted && <span className="badge cancelled">deleted</span>}
        </h1>
        <div className="row">
          {isOwner && !agent.deleted && <button onClick={() => setEditing(!editing)}>{editing ? "Close editor" : "Edit"}</button>}
          {!agent.deleted && <button onClick={() => action(`/agents/${id}/clone`, "POST")}>Clone</button>}
          {(isOwner || me?.role === "lead") && !agent.deleted && (
            <button className="danger" onClick={() => action(`/agents/${id}`, "DELETE", "Are you sure you want to delete this agent?")}>
              Delete
            </button>
          )}
        </div>
      </div>
      <p className="muted">
        {agent.provider} / {agent.model} · owner {agent.owner.username}
      </p>
      {editing ? (
        <AgentForm agent={agent} onSaved={(a) => { setAgent(a); setEditing(false); }} />
      ) : (
        <div className="panel">
          <strong>System prompt</strong>
          <pre>{agent.system_prompt || "(empty)"}</pre>
          <strong>Tools</strong>
          <ul>
            {agent.tools.map((t) => (
              <li key={t.id}>
                <code>{t.name}</code> {t.requires_approval && <span className="badge pending_approval">requires approval</span>}
                {t.deleted && <span className="badge cancelled">deleted</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      {!agent.deleted && (
        <div className="panel">
          <h2 style={{ marginTop: 0 }}>Run this agent</h2>
          <p className="muted">The run uses your own model key and your own tool secrets.</p>
          <textarea value={task} onChange={(e) => setTask(e.target.value)} placeholder="Describe the task…" />
          {missing && (
            <div className="error">
              This agent references deleted tools ({missing.join(", ")}). The run would start without them.{" "}
              <button onClick={() => start(missing)}>Run without them</button>
            </div>
          )}
          {error && <div className="error">{error}</div>}
          <div style={{ marginTop: 10 }}>
            <button className="primary" disabled={!task.trim()} onClick={() => start()}>Start run</button>
          </div>
        </div>
      )}
    </>
  );
}
