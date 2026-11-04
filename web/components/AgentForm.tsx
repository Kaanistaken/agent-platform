"use client";

import { useEffect, useState } from "react";
import { Agent, api, Tool } from "@/lib/api";

type Models = Record<string, string[]>;

// Create or edit an agent: name, system prompt, model and allowed tools (R1).
export default function AgentForm({ agent, onSaved }: { agent?: Agent; onSaved: (a: Agent) => void }) {
  const [name, setName] = useState(agent?.name ?? "");
  const [prompt, setPrompt] = useState(agent?.system_prompt ?? "");
  const [provider, setProvider] = useState(agent?.provider ?? "openai");
  const [model, setModel] = useState(agent?.model ?? "");
  const [toolIds, setToolIds] = useState<number[]>(agent?.tools.map((t) => t.id) ?? []);
  const [tools, setTools] = useState<Tool[]>([]);
  const [models, setModels] = useState<Models>({});
  const [error, setError] = useState("");

  useEffect(() => {
    api<Tool[]>("/tools").then(setTools).catch(() => {});
    api<Models>("/models").then((m) => {
      setModels(m);
      if (!agent) setModel(m["openai"]?.[0] ?? "");
    });
  }, [agent]);

  // Deleted tools still linked to the agent stay visible (D46).
  const linkedDeleted = agent?.tools.filter((t) => t.deleted) ?? [];

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    const body = { name, system_prompt: prompt, provider, model, tool_ids: toolIds };
    try {
      const saved = agent
        ? await api<Agent>(`/agents/${agent.id}`, { method: "PUT", body })
        : await api<Agent>("/agents", { method: "POST", body });
      onSaved(saved);
    } catch (err: any) {
      setError(err.message);
    }
  }

  function toggle(id: number) {
    setToolIds((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  }

  return (
    <form onSubmit={save} className="panel">
      <label>Name</label>
      <input value={name} onChange={(e) => setName(e.target.value)} />
      <label>System prompt</label>
      <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} style={{ minHeight: 140 }} />
      <div className="row">
        <div style={{ flex: 1 }}>
          <label>Provider</label>
          <select
            value={provider}
            onChange={(e) => {
              setProvider(e.target.value);
              setModel(models[e.target.value]?.[0] ?? "");
            }}
          >
            {Object.keys(models).map((p) => <option key={p}>{p}</option>)}
          </select>
        </div>
        <div style={{ flex: 1 }}>
          <label>Model</label>
          <select value={model} onChange={(e) => setModel(e.target.value)}>
            {(models[provider] ?? []).map((m) => <option key={m}>{m}</option>)}
          </select>
        </div>
      </div>
      <label>Allowed tools</label>
      {tools.map((t) => (
        <div key={t.id}>
          <label style={{ display: "inline", color: "inherit" }}>
            <input type="checkbox" style={{ width: "auto" }} checked={toolIds.includes(t.id)} onChange={() => toggle(t.id)} />{" "}
            <code>{t.name}</code> <span className="muted">{t.method} {t.url_template}</span>
            {t.requires_approval && <span className="badge pending_approval" style={{ marginLeft: 6 }}>requires approval</span>}
          </label>
        </div>
      ))}
      {linkedDeleted.map((t) => (
        <div key={t.id} className="muted">
          <input type="checkbox" style={{ width: "auto" }} checked={toolIds.includes(t.id)} onChange={() => toggle(t.id)} />{" "}
          <code>{t.name}</code> (deleted)
        </div>
      ))}
      {error && <div className="error">{error}</div>}
      <div style={{ marginTop: 14 }}>
        <button className="primary" type="submit">{agent ? "Save" : "Create agent"}</button>
      </div>
    </form>
  );
}
