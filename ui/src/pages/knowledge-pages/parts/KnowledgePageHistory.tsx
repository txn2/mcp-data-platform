import { X } from "lucide-react";
import { useKnowledgePageVersions } from "@/api/portal/hooks";

export function KnowledgePageHistory({ id, onClose }: { id: string; onClose: () => void }) {
  const { data } = useKnowledgePageVersions(id);
  return (
    <div className="rounded-lg border border-border bg-muted/40 p-4">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-sm font-medium text-foreground">Version history</span>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground">
          <X className="h-4 w-4" />
        </button>
      </div>
      <ul className="space-y-1 text-sm text-muted-foreground">
        {(data?.versions ?? []).map((v) => (
          <li key={v.id} className="flex justify-between gap-4">
            <span>
              v{v.version}
              {v.change_summary ? `: ${v.change_summary}` : ""}
            </span>
            <span className="shrink-0 text-xs">{new Date(v.created_at).toLocaleString()}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
