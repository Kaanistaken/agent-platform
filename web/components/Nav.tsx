"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState } from "react";
import { api, User } from "@/lib/api";

export default function Nav() {
  const path = usePathname();
  const [me, setMe] = useState<User | null>(null);

  useEffect(() => {
    if (path.startsWith("/login")) return;
    api<User>("/me").then(setMe).catch(() => setMe(null));
  }, [path]);

  async function logout() {
    await api("/logout", { method: "POST" }).catch(() => {});
    window.location.href = "/login";
  }

  return (
    <nav>
      <Link href="/" className="brand">Agent Platform</Link>
      {me && (
        <>
          <Link href="/">Agents</Link>
          <Link href="/tools">Tools</Link>
          <Link href="/runs">Runs</Link>
          <Link href="/settings">Settings</Link>
          <span className="spacer" />
          <span className="muted">
            {me.username} · team {me.team_name} · {me.role}
          </span>
          <button onClick={logout}>Log out</button>
        </>
      )}
    </nav>
  );
}
