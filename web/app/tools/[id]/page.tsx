"use client";

import { useParams, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import ToolForm from "@/components/ToolForm";
import { api, Tool, User } from "@/lib/api";

export default function ToolPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const [tool, setTool] = useState<Tool | null>(null);
  const [me, setMe] = useState<User | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api<Tool>(`/tools/${id}`).then(setTool).catch((e) => setError(e.message));
    api<User>("/me").then(setMe);
  }, [id]);

  async function remove() {
    if (!window.confirm("Are you sure you want to delete this tool?")) return;
    try {
      await api(`/tools/${id}`, { method: "DELETE" });
      router.push("/tools");
    } catch (e: any) {
      setError(e.message);
    }
  }

  async function clone() {
    try {
      const t = await api<Tool>(`/tools/${id}/clone`, { method: "POST" });
      router.push(`/tools/${t.id}`);
    } catch (e: any) {
      setError(e.message);
    }
  }

  if (!tool) return error ? <div className="error">{error}</div> : <p className="muted">Loading…</p>;
  const isOwner = me?.id === tool.owner.id;

  return (
    <>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <h1>
          {tool.name} {tool.deleted && <span className="badge cancelled">deleted</span>}
        </h1>
        <div className="row">
          {!tool.deleted && <button onClick={clone}>Clone</button>}
          {isOwner && !tool.deleted && <button className="danger" onClick={remove}>Delete</button>}
        </div>
      </div>
      <p className="muted">owner {tool.owner.username}{!isOwner && " · only the owner can edit; clone it to change your own copy"}</p>
      {error && <div className="error">{error}</div>}
      {isOwner && !tool.deleted ? (
        <ToolForm tool={tool} onSaved={setTool} />
      ) : (
        <div className="panel">
          <pre>{JSON.stringify(tool, null, 2)}</pre>
        </div>
      )}
    </>
  );
}
