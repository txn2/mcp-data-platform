import { useState } from "react";
import { Inbox, ClipboardCheck } from "lucide-react";
import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";
import { cn } from "@/lib/utils";
import { KIND_LABEL, STATUS_CHIP, STATUS_LABEL, formatRelative } from "./meta";

type Tab = "practitioner" | "sme";

// InboxPanel is the feedback worklist / inbox (#603): two self-scoped tabs so
// nothing is dropped — open work that needs the practitioner's resolution, and
// validation requests awaiting the SME's response.
export function InboxPanel({ onOpenThread }: { onOpenThread?: (id: string) => void }) {
  const [tab, setTab] = useState<Tab>("practitioner");
  const practitioner = usePractitionerWorklist();
  const sme = useSMEWorklist();
  const active = tab === "practitioner" ? practitioner : sme;
  const threads = active.data?.data ?? [];

  const tabBtn = (key: Tab, label: string, total: number | undefined, Icon: typeof Inbox) => (
    <button
      type="button"
      onClick={() => setTab(key)}
      className={cn(
        "flex flex-1 items-center justify-center gap-1.5 border-b-2 px-3 py-2 text-xs font-medium",
        tab === key ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      <Icon className="h-3.5 w-3.5" /> {label}
      {total ? <span className="rounded-full bg-muted px-1.5 text-[10px]">{total}</span> : null}
    </button>
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex">
        {tabBtn("practitioner", "Needs resolution", practitioner.data?.total, Inbox)}
        {tabBtn("sme", "Awaiting my validation", sme.data?.total, ClipboardCheck)}
      </div>

      <div className="flex-1 overflow-auto">
        {active.isLoading && <p className="p-3 text-xs text-muted-foreground">Loading…</p>}
        {active.isError && <p className="p-3 text-xs text-destructive">Failed to load your worklist.</p>}
        {!active.isLoading && !active.isError && threads.length === 0 && (
          <p className="p-4 text-sm text-muted-foreground">Nothing here. You're all caught up.</p>
        )}
        <ul className="divide-y">
          {threads.map((t) => (
            <li key={t.id}>
              <button
                type="button"
                onClick={() => onOpenThread?.(t.id)}
                className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent"
              >
                <span className="shrink-0 text-xs text-muted-foreground">{KIND_LABEL[t.kind]}</span>
                <span className="min-w-0 flex-1 truncate">{t.title || "(untitled feedback)"}</span>
                <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[10px] font-medium", STATUS_CHIP[t.status])}>
                  {STATUS_LABEL[t.status]}
                </span>
                {t.last_event_at && (
                  <span className="shrink-0 text-[10px] text-muted-foreground">{formatRelative(t.last_event_at)}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
