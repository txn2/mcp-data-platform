import { Paperclip } from "lucide-react";
import { usePromptsUsingResource } from "@/api/portal/hooks/attachments";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";

// UsedByPrompts lists the prompts that attach a resource (#1013), so an
// operator sees what an edit or a delete would affect before doing it. A
// deleted resource does not remove its links: the prompts keep serving and
// report the material as missing, which is exactly the outcome this list exists
// to prevent someone from causing unknowingly.
//
// The list is scoped to the caller by the server, so a personal prompt someone
// else owns is never disclosed here.
export function UsedByPrompts({ resourceId }: { resourceId: string }) {
  const { data, isError } = usePromptsUsingResource(resourceId);
  const prompts = data?.data ?? [];

  if (isError || prompts.length === 0) {
    return null;
  }

  return (
    <SectionCard
      data-testid="resource-used-by-prompts"
      title={
        <span className="flex items-center gap-1.5">
          <Paperclip className="h-3 w-3 text-muted-foreground" />
          Attached to {prompts.length} {prompts.length === 1 ? "prompt" : "prompts"}
        </span>
      }
    >
      <ul className="space-y-1">
        {prompts.map((p) => (
          <li key={p.id} className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="truncate">{p.display_name || p.name}</span>
            <Badge variant="muted" className="rounded px-1.5">
              {p.scope}
            </Badge>
          </li>
        ))}
      </ul>
      <p className="mt-2 text-xs text-muted-foreground">
        Deleting this resource leaves those prompts serving without it.
      </p>
    </SectionCard>
  );
}
