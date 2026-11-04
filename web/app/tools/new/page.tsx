"use client";

import { useRouter } from "next/navigation";
import ToolForm from "@/components/ToolForm";

export default function NewTool() {
  const router = useRouter();
  return (
    <>
      <h1>New tool</h1>
      <ToolForm onSaved={(t) => router.push(`/tools/${t.id}`)} />
    </>
  );
}
