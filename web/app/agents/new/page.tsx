"use client";

import { useRouter } from "next/navigation";
import AgentForm from "@/components/AgentForm";

export default function NewAgent() {
  const router = useRouter();
  return (
    <>
      <h1>New agent</h1>
      <AgentForm onSaved={(a) => router.push(`/agents/${a.id}`)} />
    </>
  );
}
