import { Paperclip } from "lucide-react";
import { usePromptsUsingResource } from "@/api/portal/hooks/attachments";

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
    <div className="rounded-md border bg-muted/30 p-3" data-testid="resource-used-by-prompts">
      <p className="flex items-center gap-1.5 text-xs font-medium">
        <Paperclip className="h-3 w-3 text-muted-foreground" />
        Attached to {prompts.length} {prompts.length === 1 ? "prompt" : "prompts"}
      </p>
      <ul className="mt-2 space-y-1">
        {prompts.map((p) => (
          <li key={p.id} className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="truncate">{p.display_name || p.name}</span>
            <span className="shrink-0 rounded bg-muted px-1.5 py-0.5">{p.scope}</span>
          </li>
        ))}
      </ul>
      <p className="mt-2 text-xs text-muted-foreground">
        Deleting this resource leaves those prompts serving without it.
      </p>
    </div>
  );
}
