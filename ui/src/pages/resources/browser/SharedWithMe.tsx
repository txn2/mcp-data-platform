import { Loader2 } from "lucide-react";
import { useSharedResources } from "@/api/resources/shares";
import { shortDate } from "./model";

// SharedWithMe lists the files shared with the caller (#2258), directly or
// through their persona: each with who shared it, when, and whether they may
// edit it. A row opens the file.
export function SharedWithMe({ onOpen }: { onOpen: (resourceId: string) => void }) {
  const { data, isLoading } = useSharedResources();
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <Loader2 className="mr-2 size-5 animate-spin" />
        Loading...
      </div>
    );
  }
  const files = data ?? [];
  if (files.length === 0) {
    return <p className="px-4 py-12 text-center text-sm text-muted-foreground">Nothing is shared with you.</p>;
  }
  return (
    <table className="w-full text-sm" data-testid="shared-with-me">
      <thead className="sticky top-0 bg-card text-left text-xs text-muted-foreground">
        <tr>
          <th className="px-3 py-2 font-medium">Name</th>
          <th className="px-3 py-2 font-medium">Shared by</th>
          <th className="px-3 py-2 font-medium">Access</th>
          <th className="px-3 py-2 font-medium">Shared</th>
        </tr>
      </thead>
      <tbody>
        {files.map((f) => (
          <tr
            key={f.resource.id}
            tabIndex={0}
            onClick={() => onOpen(f.resource.id)}
            onKeyDown={(e) => e.key === "Enter" && onOpen(f.resource.id)}
            className="cursor-pointer border-t hover:bg-muted/50"
          >
            <td className="px-3 py-2">{f.resource.display_name}</td>
            <td className="px-3 py-2 text-muted-foreground">
              {f.shared_by}
              {f.via_persona && <span className="block text-xs">to {f.via_persona}</span>}
            </td>
            <td className="px-3 py-2 text-muted-foreground">{f.permission === "editor" ? "Can edit" : "Can view"}</td>
            <td className="px-3 py-2 text-muted-foreground">{shortDate(f.shared_at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
