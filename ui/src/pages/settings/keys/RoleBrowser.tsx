import { useState } from "react";
import { usePersonas } from "@/api/admin/hooks";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

// RoleBrowser lists every role any persona matches on, so a key's roles are
// chosen from what the deployment actually grants rather than typed from
// memory. Extracted from KeysPage.tsx (#1206).
export function RoleBrowser({ onSelect }: { onSelect: (role: string) => void }) {
  const [open, setOpen] = useState(false);
  const { data: personaData } = usePersonas();
  const personas = personaData?.personas ?? [];

  const roleMap: { role: string; persona: string; displayName: string }[] = [];
  for (const p of personas) {
    for (const r of p.roles) {
      roleMap.push({ role: r, persona: p.name, displayName: p.display_name });
    }
  }
  roleMap.sort((a, b) => a.role.localeCompare(b.role));

  if (roleMap.length === 0) return null;

  return (
    <div>
      <Button
        type="button"
        variant="link"
        size="xs"
        className="px-0"
        onClick={() => setOpen((v) => !v)}
      >
        {open ? "Hide available roles" : "Browse available roles"}
      </Button>
      {open && (
        <div className="mt-2 max-h-40 overflow-auto rounded-md border">
          <Table className="text-xs">
            <TableHeader className="sticky top-0 bg-card">
              <TableRow className="hover:bg-transparent">
                <TableHead className="h-8 px-3 text-muted-foreground">Role</TableHead>
                <TableHead className="h-8 px-3 text-muted-foreground">
                  Persona
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {roleMap.map((r) => (
                <TableRow
                  key={`${r.role}-${r.persona}`}
                  className="cursor-pointer"
                  onClick={() => onSelect(r.role)}
                >
                  <TableCell className="px-3 py-1.5 font-mono">{r.role}</TableCell>
                  <TableCell className="px-3 py-1.5 text-muted-foreground">
                    {r.displayName}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
