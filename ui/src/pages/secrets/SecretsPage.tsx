import { useEffect, useMemo, useState } from "react";
import { KeySquare, Plus } from "lucide-react";
import { usePersonas, useSecrets } from "@/api/admin/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SearchInput } from "@/components/patterns/SearchInput";
import { relativeTime } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { placeholder } from "./secretForm";
import { filterSecrets, readSearch, writeSearch } from "./secretFilter";

// SecretsPage lists the stored secrets (#2051): what each is for, the
// connections it may be sent through and who may use it. A row opens the
// secret's editor. No value is ever shown; the server never returns one. The
// search box (#2352) narrows the table by name, description, connection and
// persona, and is kept in the address.

export function SecretsPage({
  onNavigate,
}: {
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, isError } = useSecrets();
  const open = (name: string) =>
    onNavigate(`/admin/secrets/${encodeURIComponent(name)}`);
  const secrets = useMemo(() => data?.secrets ?? [], [data]);
  const [q, setQ] = useSearchParam();
  const { data: personaData } = usePersonas();
  const personaNames = useMemo(
    () => (personaData?.personas ?? []).map((p) => p.name),
    [personaData],
  );
  const shown = useMemo(
    () => filterSecrets(secrets, q, personaNames),
    [secrets, q, personaNames],
  );

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-2">
        {secrets.length > 0 ? (
          <SearchInput
            aria-label="Search secrets"
            placeholder="Search secrets"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            className="w-full max-w-sm"
          />
        ) : (
          <span />
        )}
        <Button
          type="button"
          size="sm"
          onClick={() => onNavigate("/admin/secrets/new")}
        >
          <Plus />
          New secret
        </Button>
      </div>
      {isError && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>The secrets could not be read.</AlertDescription>
        </Alert>
      )}
      {!isLoading && !isError && secrets.length === 0 ? (
        <EmptyState icon={KeySquare} className="py-10">
          <p className="font-medium text-foreground">No secret yet.</p>
          <p className="mx-auto mt-1.5 max-w-lg">
            A secret is a value a script or an agent puts into an API request
            without ever holding it, such as a password typed into a
            vendor&rsquo;s login form. The request names it as{" "}
            {placeholder("name")}, and the platform fills it in as the request
            is sent.
          </p>
        </EmptyState>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Connections</TableHead>
                <TableHead>Personas</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.length === 0 && (
                <TableRow>
                  <TableCell
                    colSpan={5}
                    className="py-6 text-center text-sm text-muted-foreground"
                  >
                    No secrets match.
                  </TableCell>
                </TableRow>
              )}
              {shown.map((s) => (
                <TableRow
                  key={s.name}
                  className="cursor-pointer"
                  onClick={() => open(s.name)}
                >
                  <TableCell className="max-w-64 whitespace-normal break-words">
                    <div className="font-medium">{s.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">
                      {placeholder(s.name, s.kind)}
                    </div>
                  </TableCell>
                  <TableCell className="max-w-80 whitespace-normal break-words text-sm text-muted-foreground">
                    {s.description}
                  </TableCell>
                  <TableCell className="max-w-64 whitespace-normal">
                    <Names names={s.allow_connections} />
                  </TableCell>
                  <TableCell className="max-w-64 whitespace-normal">
                    {s.allow_personas.length === 0 ? (
                      <span className="text-sm text-muted-foreground">Any</span>
                    ) : (
                      <Names names={s.allow_personas} />
                    )}
                  </TableCell>
                  <TableCell
                    className="whitespace-nowrap text-sm"
                    title={s.updated_by}
                  >
                    {relativeTime(s.updated_at)}
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

// useSearchParam is the search text, read from the address once and written
// back to it on every change, so a filtered view can be linked and survives a
// reload.
function useSearchParam(): [string, (q: string) => void] {
  const [q, setQ] = useState(() =>
    typeof window === "undefined" ? "" : readSearch(window.location.search),
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = writeSearch(new URLSearchParams(window.location.search), q).toString();
    window.history.replaceState(null, "", `${window.location.pathname}${query ? `?${query}` : ""}`);
  }, [q]);
  return [q, setQ];
}

function Names({ names }: { names: string[] }) {
  return (
    <div className="flex flex-wrap gap-1">
      {names.map((n) => (
        <Badge key={n} variant="secondary" className="max-w-full font-mono" title={n}>
          {n}
        </Badge>
      ))}
    </div>
  );
}
