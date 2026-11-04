"use client";

import { useState } from "react";
import { api } from "@/lib/api";

export default function LoginPage() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    try {
      await api("/login", { method: "POST", body: { username, password } });
      window.location.href = "/";
    } catch (err: any) {
      setError(err.message);
    }
  }

  return (
    <div className="panel" style={{ maxWidth: 380, margin: "60px auto" }}>
      <h1>Log in</h1>
      <form onSubmit={submit}>
        <label>Username</label>
        <input value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />
        <label>Password</label>
        <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} />
        {error && <div className="error">{error}</div>}
        <div style={{ marginTop: 14 }}>
          <button className="primary" type="submit">Log in</button>
        </div>
      </form>
    </div>
  );
}
