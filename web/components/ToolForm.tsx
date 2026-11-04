"use client";

import { useState } from "react";
import { api, Tool } from "@/lib/api";

const help = `URL: https only; {{param}} may be a whole path segment or a whole query value.
Headers: a literal value, a whole {{param}}, or "prefix {{secret.NAME}}" (e.g. "Bearer {{secret.GITHUB_TOKEN}}").
Body: JSON; {{param}} only as a whole string value, e.g. {"title": "{{title}}"}.
Secrets are your own (Settings → Secrets) and are never shown to the model.`;

// Create or edit a tool: an HTTP request template (R2).
export default function ToolForm({ tool, onSaved }: { tool?: Tool; onSaved: (t: Tool) => void }) {
  const [name, setName] = useState(tool?.name ?? "");
  const [description, setDescription] = useState(tool?.description ?? "");
  const [method, setMethod] = useState(tool?.method ?? "GET");
  const [url, setUrl] = useState(tool?.url_template ?? "https://");
  const [headers, setHeaders] = useState(JSON.stringify(tool?.headers_template ?? {}, null, 2));
  const [body, setBody] = useState(tool?.body_template ?? "");
  const [schema, setSchema] = useState(
    JSON.stringify(tool?.params_schema ?? { type: "object", properties: {}, required: [] }, null, 2),
  );
  const [approval, setApproval] = useState(tool?.requires_approval ?? false);
  const [error, setError] = useState("");

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    let h: unknown, s: unknown;
    try {
      h = JSON.parse(headers || "{}");
      s = JSON.parse(schema);
    } catch {
      setError("Headers and parameter schema must be valid JSON.");
      return;
    }
    const payload = {
      name, description, method, url_template: url, headers_template: h,
      body_template: body, params_schema: s, requires_approval: approval,
    };
    try {
      const saved = tool
        ? await api<Tool>(`/tools/${tool.id}`, { method: "PUT", body: payload })
        : await api<Tool>("/tools", { method: "POST", body: payload });
      onSaved(saved);
    } catch (err: any) {
      setError(err.message);
    }
  }

  return (
    <form onSubmit={save} className="panel">
      <pre className="muted">{help}</pre>
      <div className="row">
        <div style={{ flex: 1 }}>
          <label>Name (the model calls the tool by this name)</label>
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div style={{ width: 140 }}>
          <label>Method</label>
          <select value={method} onChange={(e) => setMethod(e.target.value)}>
            {["GET", "POST", "PUT", "PATCH", "DELETE"].map((m) => <option key={m}>{m}</option>)}
          </select>
        </div>
      </div>
      <label>Description (shown to the model)</label>
      <input value={description} onChange={(e) => setDescription(e.target.value)} />
      <label>URL template</label>
      <input value={url} onChange={(e) => setUrl(e.target.value)} />
      <label>Headers (JSON object)</label>
      <textarea value={headers} onChange={(e) => setHeaders(e.target.value)} />
      <label>Body template (JSON, optional)</label>
      <textarea value={body} onChange={(e) => setBody(e.target.value)} />
      <label>Parameters (JSON schema, type object)</label>
      <textarea value={schema} onChange={(e) => setSchema(e.target.value)} style={{ minHeight: 140 }} />
      <label style={{ color: "inherit" }}>
        <input type="checkbox" style={{ width: "auto" }} checked={approval} onChange={(e) => setApproval(e.target.checked)} />{" "}
        Requires approval (the run pauses until the person who started it approves)
      </label>
      {error && <div className="error">{error}</div>}
      <div style={{ marginTop: 14 }}>
        <button className="primary" type="submit">{tool ? "Save" : "Create tool"}</button>
      </div>
    </form>
  );
}
