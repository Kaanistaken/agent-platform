"use client";

import { useEffect, useState } from "react";
import { api, fmtTime } from "@/lib/api";

type KeyInfo = { provider: string; set: boolean; updated_at: string };
type Secret = { id: number; name: string; allowed_host: string; updated_at: string };
type UsageLine = { username?: string; model_calls: number; input_tokens: number; output_tokens: number; calls_with_unknown_usage: number };

export default function SettingsPage() {
  const [keys, setKeys] = useState<KeyInfo[]>([]);
  const [secrets, setSecrets] = useState<Secret[]>([]);
  const [usage, setUsage] = useState<{ users: UsageLine[]; team_total?: UsageLine } | null>(null);
  const [keyInput, setKeyInput] = useState<Record<string, string>>({});
  const [sec, setSec] = useState({ name: "GITHUB_TOKEN", allowed_host: "api.github.com", value: "" });
  const [error, setError] = useState("");

  function load() {
    api<KeyInfo[]>("/me/model-keys").then(setKeys);
    api<Secret[]>("/me/secrets").then(setSecrets);
    api("/usage").then(setUsage);
  }
  useEffect(load, []);

  async function run(fn: () => Promise<unknown>) {
    setError("");
    try {
      await fn();
      load();
    } catch (e: any) {
      setError(e.message);
    }
  }

  return (
    <>
      <h1>Settings</h1>
      {error && <div className="error">{error}</div>}

      <h2>Your model API keys</h2>
      <p className="muted">Your runs use only your own key. Keys are stored encrypted and are never shown again.</p>
      <div className="panel">
        {keys.map((k) => (
          <div key={k.provider} className="row" style={{ marginBottom: 8 }}>
            <strong style={{ width: 90 }}>{k.provider}</strong>
            <span className="muted" style={{ width: 200 }}>{k.set ? `set ${fmtTime(k.updated_at)}` : "not set"}</span>
            <input
              type="password"
              style={{ flex: 1 }}
              placeholder={k.set ? "Replace key" : "Paste your key"}
              value={keyInput[k.provider] ?? ""}
              onChange={(e) => setKeyInput({ ...keyInput, [k.provider]: e.target.value })}
            />
            <button
              className="primary"
              onClick={() => run(async () => {
                await api(`/me/model-keys/${k.provider}`, { method: "PUT", body: { key: keyInput[k.provider] ?? "" } });
                setKeyInput({ ...keyInput, [k.provider]: "" });
              })}
            >
              Save
            </button>
            {k.set && (
              <button className="danger" onClick={() => window.confirm("Delete this key?") && run(() => api(`/me/model-keys/${k.provider}`, { method: "DELETE" }))}>
                Delete
              </button>
            )}
          </div>
        ))}
      </div>

      <h2>Your tool secrets</h2>
      <p className="muted">
        A secret is sent only to its allowed host, only in a header, and only in runs you start. Teammates never use it.
      </p>
      <table>
        <thead>
          <tr><th>Name</th><th>Allowed host</th><th>Updated</th><th></th></tr>
        </thead>
        <tbody>
          {secrets.map((s) => (
            <tr key={s.id}>
              <td><code>{s.name}</code></td>
              <td>{s.allowed_host}</td>
              <td className="muted">{fmtTime(s.updated_at)}</td>
              <td>
                <button className="danger" onClick={() => window.confirm("Delete this secret?") && run(() => api(`/me/secrets/${s.id}`, { method: "DELETE" }))}>
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="panel" style={{ marginTop: 10 }}>
        <div className="row">
          <div style={{ flex: 1 }}><label>Name</label><input value={sec.name} onChange={(e) => setSec({ ...sec, name: e.target.value })} /></div>
          <div style={{ flex: 1 }}><label>Allowed host</label><input value={sec.allowed_host} onChange={(e) => setSec({ ...sec, allowed_host: e.target.value })} /></div>
          <div style={{ flex: 2 }}><label>Value</label><input type="password" value={sec.value} onChange={(e) => setSec({ ...sec, value: e.target.value })} /></div>
        </div>
        <div style={{ marginTop: 10 }}>
          <button className="primary" onClick={() => run(async () => {
            await api("/me/secrets", { method: "POST", body: sec });
            setSec({ ...sec, value: "" });
          })}>Add secret</button>
        </div>
      </div>

      <h2>Token usage</h2>
      {usage && (
        <table>
          <thead>
            <tr><th>User</th><th>Model calls</th><th>Input tokens</th><th>Output tokens</th><th>Calls with unknown usage</th></tr>
          </thead>
          <tbody>
            {usage.users?.map((u) => (
              <tr key={u.username}>
                <td>{u.username}</td><td>{u.model_calls}</td><td>{u.input_tokens}</td><td>{u.output_tokens}</td><td>{u.calls_with_unknown_usage}</td>
              </tr>
            ))}
            {usage.team_total && (
              <tr>
                <td><strong>Team total</strong></td><td>{usage.team_total.model_calls}</td><td>{usage.team_total.input_tokens}</td>
                <td>{usage.team_total.output_tokens}</td><td>{usage.team_total.calls_with_unknown_usage}</td>
              </tr>
            )}
          </tbody>
        </table>
      )}
    </>
  );
}
