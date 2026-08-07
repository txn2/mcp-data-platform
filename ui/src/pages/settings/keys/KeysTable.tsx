import { Trash2, X } from "lucide-react";
import type { APIKeySummary } from "@/api/admin/types";
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
import { cn } from "@/lib/utils";

function formatExpiration(expiresAt?: string): string {
  if (!expiresAt) return "Never";
  return new Date(expiresAt).toLocaleDateString();
}

export interface KeysTableProps {
  keys: APIKeySummary[];
  isReadOnly: boolean;
  deleteConfirm: string | null;
  deleting: boolean;
  onRequestDelete: (name: string) => void;
  onCancelDelete: () => void;
  onConfirmDelete: (name: string) => void;
}

// KeysTable lists the configured API keys. Extracted from KeysPage.tsx (#1206).
export function KeysTable({
  keys,
  isReadOnly,
  deleteConfirm,
  deleting,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: KeysTableProps) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="bg-muted/30 text-xs text-muted-foreground hover:bg-muted/30">
          <TableHead className="px-5">Name</TableHead>
          <TableHead className="px-5">Email</TableHead>
          <TableHead className="px-5">Description</TableHead>
          <TableHead className="px-5">Roles</TableHead>
          <TableHead className="px-5">Expiration</TableHead>
          {!isReadOnly && <TableHead className="w-20 px-5">Actions</TableHead>}
        </TableRow>
      </TableHeader>
      <TableBody>
        {keys.map((k) => (
          <KeyRow
            key={k.name}
            apiKey={k}
            isReadOnly={isReadOnly}
            confirming={deleteConfirm === k.name}
            deleting={deleting}
            onRequestDelete={onRequestDelete}
            onCancelDelete={onCancelDelete}
            onConfirmDelete={onConfirmDelete}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function KeyRow({
  apiKey: k,
  isReadOnly,
  confirming,
  deleting,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: {
  apiKey: APIKeySummary;
  isReadOnly: boolean;
  confirming: boolean;
  deleting: boolean;
  onRequestDelete: (name: string) => void;
  onCancelDelete: () => void;
  onConfirmDelete: (name: string) => void;
}) {
  // ui/table sets whitespace-nowrap on every cell; with six columns the prose
  // ones have to wrap, or the Actions column is pushed out of the card and
  // behind a horizontal scroll.
  return (
    <TableRow className={cn(k.expired && "opacity-50")}>
      <TableCell className="whitespace-normal px-5 py-3 font-medium">
        <KeyName apiKey={k} />
      </TableCell>
      <TableCell className="whitespace-normal break-all px-5 py-3 text-muted-foreground">
        {k.email || <span className="italic opacity-50">--</span>}
      </TableCell>
      <TableCell className="max-w-[18rem] truncate px-5 py-3 text-muted-foreground">
        {k.description || <span className="italic opacity-50">--</span>}
      </TableCell>
      <TableCell className="whitespace-normal px-5 py-3">
        <KeyRoles roles={k.roles} />
      </TableCell>
      <TableCell className="px-5 py-3 text-muted-foreground">
        {formatExpiration(k.expires_at)}
      </TableCell>
      {!isReadOnly && (
        <TableCell className="px-5 py-3">
          <KeyActions
            name={k.name}
            source={k.source}
            confirming={confirming}
            deleting={deleting}
            onRequestDelete={onRequestDelete}
            onCancelDelete={onCancelDelete}
            onConfirmDelete={onConfirmDelete}
          />
        </TableCell>
      )}
    </TableRow>
  );
}

// KeyName is the key's identity plus the two facts that qualify it: where it
// is defined (which decides whether it can be deleted here) and whether it has
// lapsed.
function KeyName({ apiKey: k }: { apiKey: APIKeySummary }) {
  return (
    <div className="flex items-center gap-2">
      <span className={cn(k.expired && "line-through")}>{k.name}</span>
      {k.source && (
        <Badge
          variant={k.source === "file" ? "muted" : "info"}
          className="rounded px-1"
        >
          {k.source === "file" ? "file" : "database"}
        </Badge>
      )}
      {k.expired && <Badge variant="danger">Expired</Badge>}
    </div>
  );
}

function KeyRoles({ roles }: { roles: string[] }) {
  if (roles.length === 0) {
    return (
      <span className="text-xs italic text-muted-foreground opacity-50">None</span>
    );
  }
  return (
    <div className="flex flex-wrap gap-1">
      {roles.map((r) => (
        <Badge key={r} variant="outline">
          {r}
        </Badge>
      ))}
    </div>
  );
}

// KeyActions is the delete affordance and its inline confirm. A file-sourced
// key has no action at all: it is owned by the config file, and saying so is
// more useful than a disabled button.
function KeyActions({
  name,
  source,
  confirming,
  deleting,
  onRequestDelete,
  onCancelDelete,
  onConfirmDelete,
}: {
  name: string;
  source?: string;
  confirming: boolean;
  deleting: boolean;
  onRequestDelete: (name: string) => void;
  onCancelDelete: () => void;
  onConfirmDelete: (name: string) => void;
}) {
  if (source === "file") {
    return <span className="text-xs italic text-muted-foreground">config file</span>;
  }
  if (confirming) {
    return (
      <div className="flex items-center gap-1.5">
        <Button
          type="button"
          variant="destructive"
          size="xs"
          onClick={() => onConfirmDelete(name)}
          disabled={deleting}
        >
          {deleting ? "..." : "Confirm"}
        </Button>
        <Button
          type="button"
          variant="outline"
          size="icon-xs"
          aria-label={`Cancel deleting ${name}`}
          onClick={onCancelDelete}
        >
          <X />
        </Button>
      </div>
    );
  }
  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      onClick={() => onRequestDelete(name)}
      className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
    >
      <Trash2 />
      Delete
    </Button>
  );
}
