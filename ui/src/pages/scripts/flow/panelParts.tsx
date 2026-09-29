import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { ROLE_COLOR } from "./flowModel";

// The pieces the Flow tab's side panel is built from (#1906, #1972): a table
// of labelled rows, a source excerpt, and a role's color swatch.

// Swatch is a role's color, beside its name.
export function Swatch({ role }: { role: keyof typeof ROLE_COLOR }) {
  return (
    <i
      className="mr-1 inline-block size-2.5 rounded-sm align-[-1px]"
      style={{ background: ROLE_COLOR[role] }}
    />
  );
}


export function Rows({ children }: { children: ReactNode }) {
  return <dl className="grid grid-cols-[5rem_1fr] gap-x-2 gap-y-1.5 text-xs">{children}</dl>;
}

export function Row({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 break-words", mono && "font-mono")}>{children}</dd>
    </>
  );
}

export function Lines({ items }: { items: string[] }) {
  return (
    <>
      {items.map((t, i) => (
        <div key={i}>{t}</div>
      ))}
    </>
  );
}

export function Excerpt({ text }: { text: string }) {
  if (!text) return null;
  return (
    <pre className="max-h-48 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px] leading-relaxed whitespace-pre">
      {text}
    </pre>
  );
}

