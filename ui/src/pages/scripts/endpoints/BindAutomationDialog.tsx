import { useState } from "react";
import { useScriptListing } from "@/api/portal/hooks/scriptListFilter";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { useDebounced } from "@/lib/useDebounced";

// BindAutomationDialog picks the automation an endpoint will run (#2208). The
// binding itself is made on the automation's page, in its Endpoint section,
// which is the one form that sets one: it refuses an automation that defines
// no handle(req), naming what is missing.

export function BindAutomationDialog({
  open,
  onOpenChange,
  onNavigate,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onNavigate: (path: string) => void;
}) {
  const [search, setSearch] = useState("");
  const debounced = useDebounced(search, 250);
  const { data, isLoading } = useScriptListing({
    search: debounced,
    kind: "script",
    sort: "name",
    dir: "asc",
  });
  const rows = (data?.data ?? []).slice(0, 20);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Bind an automation</DialogTitle>
          <DialogDescription>
            Pick the automation the endpoint runs. It must define handle(req).
            Its page opens; bind it in its Endpoint section, where the address,
            access and limits are set.
          </DialogDescription>
        </DialogHeader>
        <Input
          aria-label="Search automations"
          placeholder="Search automations"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <ul className="max-h-72 space-y-1 overflow-y-auto">
          {isLoading && (
            <li className="text-sm text-muted-foreground">Loading...</li>
          )}
          {!isLoading && rows.length === 0 && (
            <li className="text-sm text-muted-foreground">
              No automation matches.
            </li>
          )}
          {rows.map(({ script: s }) => (
            <li key={s.id}>
              <Button
                variant="ghost"
                className="h-auto w-full justify-between px-2 py-1.5 text-left"
                onClick={() => {
                  onOpenChange(false);
                  onNavigate(`/admin/automations/${encodeURIComponent(s.id)}`);
                }}
              >
                <span className="font-medium">{s.display_name || s.name}</span>
                <span className="text-xs text-muted-foreground">
                  {s.owner_email}
                </span>
              </Button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
