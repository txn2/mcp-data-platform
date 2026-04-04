import { useMemo } from "react";
import { usePersonas } from "@/api/admin/hooks";
import { Shield } from "lucide-react";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface RoleRow {
  role: string;
  personaName: string;
  displayName: string;
  toolCount: number;
}

// ---------------------------------------------------------------------------
// RolesPage
// ---------------------------------------------------------------------------

export function RolesPage() {
  const { data: personaList, isLoading } = usePersonas();
  const personas = personaList?.personas ?? [];

  // Flatten: each role in each persona becomes its own row, sorted by role.
  const rows = useMemo<RoleRow[]>(() => {
    const result: RoleRow[] = [];
    for (const p of personas) {
      for (const role of p.roles) {
        result.push({
          role,
          personaName: p.name,
          displayName: p.display_name,
          toolCount: p.tool_count,
        });
      }
    }
    result.sort((a, b) => a.role.localeCompare(b.role));
    return result;
  }, [personas]);

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col overflow-hidden rounded-lg border bg-card">
      {/* Header */}
      <div className="border-b px-5 py-3">
        <h3 className="text-sm font-semibold leading-none">Roles</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Role to persona mappings across the platform
        </p>
      </div>

      {/* Table */}
      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : rows.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Shield className="mb-3 h-8 w-8 opacity-30" />
            <p className="text-sm">No roles configured</p>
            <p className="mt-1 text-xs opacity-60">
              Roles are defined through persona configurations
            </p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/30 text-left text-xs font-medium text-muted-foreground">
                <th className="px-5 py-2">Role</th>
                <th className="px-5 py-2">Persona</th>
                <th className="px-5 py-2">Display Name</th>
                <th className="px-5 py-2 text-right">Tool Count</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {rows.map((row) => (
                <tr
                  key={`${row.role}-${row.personaName}`}
                  className="transition-colors hover:bg-muted/20"
                >
                  <td className="px-5 py-3">
                    <span className="rounded-full border bg-muted/50 px-2.5 py-0.5 text-xs font-medium">
                      {row.role}
                    </span>
                  </td>
                  <td className="px-5 py-3 font-mono text-xs">
                    {row.personaName}
                  </td>
                  <td className="px-5 py-3 text-muted-foreground">
                    {row.displayName}
                  </td>
                  <td className="px-5 py-3 text-right tabular-nums text-muted-foreground">
                    {row.toolCount}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
