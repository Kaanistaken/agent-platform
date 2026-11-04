"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, Tool } from "@/lib/api";

export default function ToolsPage() {
  const [tools, setTools] = useState<Tool[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    api<Tool[]>("/tools").then(setTools).catch((e) => setError(e.message));
  }, []);
  return (
    <>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h1>Tools</h1>
        <Link href="/tools/new"><button className="primary">New tool</button></Link>
      </div>
      {error && <div className="error">{error}</div>}
      <table>
        <thead>
          <tr><th>Name</th><th>Request</th><th>Approval</th><th>Owner</th></tr>
        </thead>
        <tbody>
          {tools.map((t) => (
            <tr key={t.id}>
              <td><Link href={`/tools/${t.id}`}>{t.name}</Link></td>
              <td><code>{t.method} {t.url_template}</code></td>
              <td>{t.requires_approval ? "required" : "-"}</td>
              <td>{t.owner.username}</td>
            </tr>
          ))}
          {tools.length === 0 && <tr><td colSpan={4} className="muted">No tools yet.</td></tr>}
        </tbody>
      </table>
    </>
  );
}
