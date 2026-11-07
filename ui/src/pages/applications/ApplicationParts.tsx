import { useState } from "react";
import { Boxes, CircleMinus, Copy, Ellipsis, ExternalLink, Plus } from "lucide-react";
import {
  useRemoveApplicationPart,
  type ApplicationDetail,
  type ApplicationPartView,
} from "@/api/portal/hooks/applications";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { AddPartsDialog } from "./AddPartsDialog";
import { PartTile } from "./PartTile";
import {
  PART_GROUP_LABEL,
  PART_KINDS,
  PART_KIND_LABEL,
  errorText,
  partDescription,
  partName,
  partPath,
  partReference,
} from "./applicationFormat";

// ApplicationParts is what an application is made of (#2411, #2527): each
// part by name, grouped by kind, with the version its current release pins
// and whether a newer one exists. The owner and administrators add and remove
// parts; a part belongs to at most one application.

interface Props {
  detail: ApplicationDetail;
  canEdit: boolean;
  isAdmin: boolean;
  onNavigate: (path: string) => void;
}

export function ApplicationParts({ detail, canEdit, isAdmin, onNavigate }: Props) {
  const app = detail.application;
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<ApplicationPartView | null>(null);
  const pinned = new Map((detail.current_release?.pins ?? []).map((p) => [`${p.kind}/${p.part_id}`, p.version]));
  const parts = detail.parts ?? [];
  const behind = parts.filter((p) => isBehind(p, pinned.get(`${p.kind}/${p.id}`))).length;

  return (
    <SectionCard
      title={
        <div className="flex flex-col gap-0.5">
          <span>Parts</span>
          <span className="text-sm font-normal text-muted-foreground" data-testid="parts-subtitle">
            {partsSummary(parts.length, app.current_release)}
          </span>
        </div>
      }
      action={
        canEdit && (
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus aria-hidden="true" />
            Add parts
          </Button>
        )
      }
      data-testid="application-parts"
    >
      {parts.length === 0 ? (
        <EmptyState icon={Boxes}>No parts yet</EmptyState>
      ) : (
        <div className="-mx-4">
          <PartGroups
            parts={parts}
            pinned={pinned}
            isAdmin={isAdmin}
            canEdit={canEdit}
            onRemove={setRemoving}
            onNavigate={onNavigate}
          />
          {behind > 0 && <BehindFooter behind={behind} release={app.current_release} />}
        </div>
      )}
      {adding && <AddPartsDialog app={app.id} appName={app.name} onNavigate={onNavigate} onClose={() => setAdding(false)} />}
      {removing && (
        <RemovePartDialog app={app.id} appName={app.name} part={removing} onClose={() => setRemoving(null)} />
      )}
    </SectionCard>
  );
}

/** PartGroups lists the parts under a heading per kind, a kind with none left out. */
function PartGroups({
  parts,
  pinned,
  isAdmin,
  canEdit,
  onRemove,
  onNavigate,
}: {
  parts: ApplicationPartView[];
  pinned: Map<string, number>;
  isAdmin: boolean;
  canEdit: boolean;
  onRemove: (part: ApplicationPartView) => void;
  onNavigate: (path: string) => void;
}) {
  return (
    <>
      {PART_KINDS.map((kind) => {
        const group = parts.filter((p) => p.kind === kind);
        if (group.length === 0) return null;
        return (
          <div key={kind} role="group" aria-label={PART_GROUP_LABEL[kind]}>
            <div className="border-t px-4 pt-2.5 pb-1.5 text-xs font-semibold tracking-wider text-muted-foreground uppercase">
              {PART_GROUP_LABEL[kind]} · {group.length}
            </div>
            {group.map((part) => (
              <PartRow
                key={`${part.kind}/${part.id}`}
                part={part}
                pinned={pinned.get(`${part.kind}/${part.id}`)}
                path={part.state === "ok" ? partPath(part.kind, part.id, isAdmin) : null}
                canEdit={canEdit}
                onRemove={() => onRemove(part)}
                onNavigate={onNavigate}
              />
            ))}
          </div>
        );
      })}
    </>
  );
}

/** BehindFooter says how many parts have a version newer than the release pins. */
function BehindFooter({ behind, release }: { behind: number; release: number }) {
  return (
    <div
      className="-mb-4 flex items-center justify-between gap-4 rounded-b-xl border-t bg-muted/40 px-4 py-3 text-sm"
      data-testid="parts-behind"
    >
      <span className="text-muted-foreground">
        {behind === 1 ? "1 part has" : `${behind} parts have`} a newer version than release v{release} pins. Publish a
        release to pin {behind === 1 ? "it" : "them"}.
      </span>
      <button
        type="button"
        className="shrink-0 font-medium text-primary hover:underline"
        onClick={() => document.getElementById("application-releases")?.scrollIntoView({ behavior: "smooth", block: "start" })}
      >
        Review changes
      </button>
    </div>
  );
}

function partsSummary(count: number, release: number): string {
  const n = count === 1 ? "1 part." : `${count} parts.`;
  return release > 0 ? `${n} Release v${release} pins the version of each.` : `${n} No release published yet.`;
}

/** isBehind reports a part whose newest version is past the one its release pins. */
function isBehind(p: ApplicationPartView, pinned: number | undefined): boolean {
  return pinned !== undefined && p.latest_version !== null && p.latest_version > pinned;
}

function PartRow({
  part,
  pinned,
  path,
  canEdit,
  onRemove,
  onNavigate,
}: {
  part: ApplicationPartView;
  pinned: number | undefined;
  path: string | null;
  canEdit: boolean;
  onRemove: () => void;
  onNavigate: (path: string) => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const ok = part.state === "ok";
  const hasMenu = canEdit || (ok && (path !== null || partReference(part.kind, part.id) !== null));
  return (
    <div
      data-testid={`part-${part.kind}-${part.id}`}
      className={cn(
        "grid grid-cols-[36px_minmax(0,1fr)_auto_36px] items-center gap-4 border-t px-4 py-3 transition-colors hover:bg-muted/40",
        menuOpen && "bg-muted/40",
      )}
    >
      <PartTile kind={part.kind} muted={!ok} />
      <PartNameCell part={part} path={path} onNavigate={onNavigate} />
      <PartVersion part={part} pinned={pinned} />
      {hasMenu ? (
        <PartMenu
          part={part}
          path={path}
          canEdit={canEdit}
          open={menuOpen}
          onOpenChange={setMenuOpen}
          onRemove={onRemove}
          onNavigate={onNavigate}
        />
      ) : (
        <span />
      )}
    </div>
  );
}

/** PartNameCell is the part's name, a link where it opens, and its description line. */
function PartNameCell({
  part,
  path,
  onNavigate,
}: {
  part: ApplicationPartView;
  path: string | null;
  onNavigate: (path: string) => void;
}) {
  const name = partName(part);
  const description = partDescription(part);
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      {path ? (
        <button type="button" className="truncate text-left text-sm font-medium hover:underline" onClick={() => onNavigate(path)}>
          {name}
        </button>
      ) : (
        <span className={cn("truncate text-sm font-medium", part.state !== "ok" && "text-muted-foreground")}>{name}</span>
      )}
      {description && <span className="truncate text-[13px] text-muted-foreground">{description}</span>}
    </div>
  );
}

/** PartMenu is a row's actions: open, copy its reference, remove. */
function PartMenu({
  part,
  path,
  canEdit,
  open,
  onOpenChange,
  onRemove,
  onNavigate,
}: {
  part: ApplicationPartView;
  path: string | null;
  canEdit: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRemove: () => void;
  onNavigate: (path: string) => void;
}) {
  const ok = part.state === "ok";
  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button size="icon" variant="ghost" aria-label={`Actions for ${partName(part)}`}>
          <Ellipsis aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        {ok && path && (
          <DropdownMenuItem onSelect={() => onNavigate(path)}>
            <ExternalLink aria-hidden="true" />
            Open {PART_KIND_LABEL[part.kind].toLowerCase()}
          </DropdownMenuItem>
        )}
        {ok && <CopyReferenceItem part={part} />}
        {canEdit && ok && <DropdownMenuSeparator />}
        {canEdit && (
          <DropdownMenuItem variant="destructive" onSelect={onRemove}>
            <CircleMinus aria-hidden="true" />
            Remove from application
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function CopyReferenceItem({ part }: { part: ApplicationPartView }) {
  const ref = partReference(part.kind, part.id);
  if (!ref) return null;
  return (
    <DropdownMenuItem onSelect={() => void navigator.clipboard?.writeText(ref).catch(() => undefined)}>
      <Copy aria-hidden="true" />
      Copy reference
    </DropdownMenuItem>
  );
}

function PartVersion({ part, pinned }: { part: ApplicationPartView; pinned: number | undefined }) {
  if (part.kind === "source") {
    return <span className="text-[13px] text-muted-foreground">Not versioned</span>;
  }
  const latest = part.latest_version;
  if (pinned === undefined) {
    return (
      <span className="text-[13px] text-muted-foreground">
        {latest !== null ? `Not pinned · v${latest} latest` : "Not pinned"}
      </span>
    );
  }
  if (latest !== null && latest > pinned) {
    return (
      <div className="flex flex-col items-start gap-1">
        <span className="text-[13px]">Pinned v{pinned}</span>
        <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/10 px-2 py-0.5 text-xs text-amber-700 dark:text-amber-300">
          <span aria-hidden="true" className="size-1.5 rounded-full bg-amber-500" />v{latest} is the latest
        </span>
      </div>
    );
  }
  return <span className="text-[13px]">Pinned v{pinned}{latest === pinned ? " · latest" : ""}</span>;
}

function RemovePartDialog({
  app,
  appName,
  part,
  onClose,
}: {
  app: string;
  appName: string;
  part: ApplicationPartView;
  onClose: () => void;
}) {
  const remove = useRemoveApplicationPart(app);
  const [failure, setFailure] = useState<string | null>(null);
  const name = partName(part);
  const confirm = () => {
    setFailure(null);
    remove.mutate(
      { kind: part.kind, id: part.id },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The part could not be removed")) },
    );
  };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Remove part</DialogTitle>
          <DialogDescription>
            Remove {name} from {appName}? It stays in the portal; the next release no longer pins it.
          </DialogDescription>
        </DialogHeader>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={remove.isPending}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={confirm} disabled={remove.isPending}>
            Remove
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
