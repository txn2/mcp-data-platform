import { useState } from "react";
import { Boxes } from "lucide-react";
import {
  useAddApplicationPart,
  useRemoveApplicationPart,
  type ApplicationDetail,
  type ApplicationPart,
  type ApplicationPartKind,
} from "@/api/portal/hooks/applications";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { PART_KINDS, PART_KIND_LABEL, errorText, partPath, versionLabel } from "./applicationFormat";
import { Field } from "./NewApplicationDialog";

// ApplicationParts is what an application is made of (#2411), each part with
// the version its current release pins. The owner and administrators add and
// remove parts; a part belongs to at most one application, and the server
// says which one when a part is already taken.

interface Props {
  detail: ApplicationDetail;
  canEdit: boolean;
  isAdmin: boolean;
  onNavigate: (path: string) => void;
}

export function ApplicationParts({ detail, canEdit, isAdmin, onNavigate }: Props) {
  const app = detail.application;
  const remove = useRemoveApplicationPart(app.id);
  const [adding, setAdding] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const pinned = new Map(
    (detail.current_release?.pins ?? []).map((p) => [`${p.kind}/${p.part_id}`, p.version]),
  );

  const onRemove = (part: ApplicationPart) => {
    setFailure(null);
    remove.mutate(
      { kind: part.kind, id: part.id },
      { onError: (e) => setFailure(errorText(e, "The part could not be removed")) },
    );
  };

  return (
    <SectionCard
      title="Parts"
      action={
        canEdit && (
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            Add part
          </Button>
        )
      }
    >
      {failure && (
        <Alert variant="destructive" className="mb-3">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {app.parts.length === 0 ? (
        <EmptyState icon={Boxes}>No parts</EmptyState>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Kind</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>Version</TableHead>
              {canEdit && <TableHead className="w-0" />}
            </TableRow>
          </TableHeader>
          <TableBody>
            {app.parts.map((part) => (
              <PartRow
                key={`${part.kind}/${part.id}`}
                part={part}
                version={pinned.get(`${part.kind}/${part.id}`)}
                path={partPath(part.kind, part.id, isAdmin)}
                canEdit={canEdit}
                removing={remove.isPending}
                onRemove={() => onRemove(part)}
                onNavigate={onNavigate}
              />
            ))}
          </TableBody>
        </Table>
      )}
      {adding && <AddPartDialog app={app.id} onClose={() => setAdding(false)} />}
    </SectionCard>
  );
}

function PartRow({
  part,
  version,
  path,
  canEdit,
  removing,
  onRemove,
  onNavigate,
}: {
  part: ApplicationPart;
  version: number | undefined;
  path: string | null;
  canEdit: boolean;
  removing: boolean;
  onRemove: () => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <TableRow data-testid={`part-${part.kind}-${part.id}`}>
      <TableCell className="text-sm">{PART_KIND_LABEL[part.kind] ?? part.kind}</TableCell>
      <TableCell className="font-mono text-xs">
        {path ? (
          <button type="button" className="text-primary hover:underline" onClick={() => onNavigate(path)}>
            {part.id}
          </button>
        ) : (
          part.id
        )}
      </TableCell>
      <TableCell className="text-sm">{versionLabel(version ?? 0)}</TableCell>
      {canEdit && (
        <TableCell>
          <Button size="sm" variant="ghost" disabled={removing} onClick={onRemove}>
            Remove
          </Button>
        </TableCell>
      )}
    </TableRow>
  );
}

function AddPartDialog({ app, onClose }: { app: string; onClose: () => void }) {
  const add = useAddApplicationPart(app);
  const [kind, setKind] = useState<ApplicationPartKind>("script");
  const [id, setId] = useState("");
  const [failure, setFailure] = useState<string | null>(null);

  const submit = () => {
    setFailure(null);
    add.mutate(
      { kind, id: id.trim() },
      {
        onSuccess: onClose,
        onError: (e) => setFailure(errorText(e, "The part could not be added")),
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add part</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Field id="part-kind" label="Kind">
            <Select value={kind} onValueChange={(v) => setKind(v as ApplicationPartKind)}>
              <SelectTrigger id="part-kind" aria-label="Kind" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {PART_KINDS.map((k) => (
                  <SelectItem key={k} value={k}>
                    {PART_KIND_LABEL[k]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field id="part-id" label="ID">
            <Input id="part-id" value={id} onChange={(e) => setId(e.target.value)} />
          </Field>
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={add.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={id.trim() === "" || add.isPending}>
            Add part
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
