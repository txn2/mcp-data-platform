import { X } from "lucide-react";
import { useKnowledgePageVersions } from "@/api/portal/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";

export function KnowledgePageHistory({ id, onClose }: { id: string; onClose: () => void }) {
  const { data } = useKnowledgePageVersions(id);
  return (
    <SectionCard
      title="Version history"
      action={
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Close version history"
          onClick={onClose}
        >
          <X />
        </Button>
      }
    >
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
    </SectionCard>
  );
}
