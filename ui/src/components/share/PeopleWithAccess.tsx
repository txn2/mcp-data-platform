import type { ReactNode } from "react";
import { ChevronDown, Users } from "lucide-react";
import { useDirectoryUsers, type SharePersona } from "@/api/portal/hooks";
import type { DirectoryUser, Share, SharePermission } from "@/api/portal/types";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { accessEnds, GUEST_TINT, initials, recipientOf, roleLabel, tintFor } from "./model";

/** The person who owns the item, as the dialog names them. */
export interface Owner {
  email: string;
  /** The viewer is the owner. */
  you: boolean;
}

/** fullName is a directory entry's name, or "". */
export function fullName(u: DirectoryUser | undefined): string {
  return u ? [u.first_name, u.last_name].filter(Boolean).join(" ") : "";
}

/**
 * usePerson is what the directory holds for an address: their name, and
 * whether they have an account at all. Until the directory answers, and when
 * it cannot, nobody is a guest: the Guest pill is only ever a positive answer.
 */
export function usePerson(email: string) {
  const { data, isSuccess } = useDirectoryUsers(email, email !== "", { limit: 5 });
  const match = data?.users.find((u) => u.email.toLowerCase() === email.toLowerCase());
  return { name: fullName(match), guest: isSuccess && email !== "" && !match };
}

/** GuestPill marks an address with no account. */
export function GuestPill() {
  return (
    <span className="ml-1.5 inline-flex items-center rounded-full border border-amber-500 px-1.5 text-[11px] leading-4 font-semibold text-amber-800 dark:border-amber-400 dark:text-amber-200">
      Guest
    </span>
  );
}

/** Row is one line of People with access: a mark, two lines, and a control. */
function Row({ mark, title, detail, control }: { mark: ReactNode; title: ReactNode; detail: ReactNode; control: ReactNode }) {
  return (
    <div className="flex min-h-[52px] items-center gap-3" data-testid="access-row">
      {mark}
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm">{title}</div>
        {detail && <div className="mt-0.5 truncate text-xs text-muted-foreground">{detail}</div>}
      </div>
      {control}
    </div>
  );
}

/**
 * PeopleWithAccess lists who can open the item: the owner, then each person
 * and group in the order they were added, each with what they can do.
 */
export function PeopleWithAccess({
  owner,
  grants,
  personas,
  loading,
  now,
  onRole,
  onRemove,
}: {
  owner: Owner | undefined;
  grants: Share[];
  personas: SharePersona[];
  loading: boolean;
  now: number;
  onRole: (share: Share, permission: SharePermission) => void;
  onRemove: (share: Share, name: string) => void;
}) {
  return (
    <section aria-labelledby="share-people-label">
      <h3 id="share-people-label" className="mb-1.5 text-[13px] font-semibold text-foreground/80">
        People with access
      </h3>
      {loading ? (
        <div className="space-y-2" data-testid="access-loading">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex h-[52px] items-center gap-3">
              <Skeleton className="size-9 rounded-full" />
              <Skeleton className="h-4 flex-1" />
            </div>
          ))}
        </div>
      ) : (
        <>
          {owner && <OwnerRow owner={owner} />}
          {grants.map((g) =>
            g.shared_with_persona ? (
              <GroupRow key={g.id} share={g} personas={personas} now={now} onRole={onRole} onRemove={onRemove} />
            ) : (
              <PersonRow key={g.id} share={g} now={now} onRole={onRole} onRemove={onRemove} />
            ),
          )}
          {grants.length === 0 && <p className="pt-1 text-xs text-muted-foreground">Not shared with anyone yet.</p>}
        </>
      )}
    </section>
  );
}

function OwnerRow({ owner }: { owner: Owner }) {
  const person = usePerson(owner.email);
  const name = person.name || owner.email;
  return (
    <Row
      mark={<Avatar tint={tintFor(owner.email)}>{initials(person.name, owner.email)}</Avatar>}
      title={owner.you ? `${name} (you)` : name}
      detail={person.name ? owner.email : ""}
      control={<span className="px-2 text-sm text-muted-foreground">Owner</span>}
    />
  );
}

function PersonRow({
  share,
  now,
  onRole,
  onRemove,
}: {
  share: Share;
  now: number;
  onRole: (share: Share, permission: SharePermission) => void;
  onRemove: (share: Share, name: string) => void;
}) {
  const email = recipientOf(share);
  const person = usePerson(share.shared_with_email ?? "");
  const ends = accessEnds(share.expires_at, now);
  if (person.guest) {
    return (
      <Row
        mark={<Avatar tint={GUEST_TINT}>{initials("", email)}</Avatar>}
        title={
          <>
            {email}
            <GuestPill />
          </>
        }
        detail={ends || "No account. Opens it from a link sent by email."}
        control={<RoleMenu share={share} name={email} guest onRole={onRole} onRemove={onRemove} />}
      />
    );
  }
  const name = person.name || email;
  return (
    <Row
      mark={<Avatar tint={tintFor(email)}>{initials(person.name, email)}</Avatar>}
      title={name}
      detail={[person.name ? email : "", ends].filter(Boolean).join(" · ")}
      control={<RoleMenu share={share} name={name} onRole={onRole} onRemove={onRemove} />}
    />
  );
}

function GroupRow({
  share,
  personas,
  now,
  onRole,
  onRemove,
}: {
  share: Share;
  personas: SharePersona[];
  now: number;
  onRole: (share: Share, permission: SharePermission) => void;
  onRemove: (share: Share, name: string) => void;
}) {
  const p = personas.find((x) => x.name === share.shared_with_persona);
  const name = p?.display_name || share.shared_with_persona || "";
  const count = p?.member_count;
  const size = count === undefined ? "Group" : `Group · ${count} ${count === 1 ? "person" : "people"}`;
  return (
    <Row
      mark={
        <Avatar tint={tintFor(name)}>
          <Users />
        </Avatar>
      }
      title={name}
      detail={[size, accessEnds(share.expires_at, now)].filter(Boolean).join(" · ")}
      control={<RoleMenu share={share} name={name} onRole={onRole} onRemove={onRemove} />}
    />
  );
}

/** RoleMenu is what one grant can do, changed at once, and its removal. */
function RoleMenu({
  share,
  name,
  guest = false,
  onRole,
  onRemove,
}: {
  share: Share;
  name: string;
  guest?: boolean;
  onRole: (share: Share, permission: SharePermission) => void;
  onRemove: (share: Share, name: string) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="h-9 shrink-0 text-sm" aria-label={`What ${name} can do: ${roleLabel(share.permission)}`}>
          {roleLabel(share.permission)}
          <ChevronDown />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl p-1.5">
        <DropdownMenuItem className="rounded-lg" onSelect={() => share.permission !== "viewer" && onRole(share, "viewer")}>
          Can view
        </DropdownMenuItem>
        <DropdownMenuItem
          className="flex-col items-start gap-0 rounded-lg"
          disabled={guest}
          onSelect={() => share.permission !== "editor" && onRole(share, "editor")}
        >
          Can edit
          {guest && <span className="text-xs text-muted-foreground">Guests can only view.</span>}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem className="rounded-lg text-destructive focus:text-destructive" onSelect={() => onRemove(share, name)}>
          Remove access
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
