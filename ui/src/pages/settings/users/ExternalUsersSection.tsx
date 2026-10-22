import { useId, useState } from "react";
import { Plus, ChevronUp } from "lucide-react";
import {
  useCreateExternalUser,
  useExternalUserAction,
  useExternalUsers,
  usePersonas,
} from "@/api/admin/hooks";
import type { ExternalUser } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

// ExternalUsersSection is Admin > Users' list of the accounts the platform
// created for people outside the organization (#2092): who they are, their
// external persona, what is shared with them, when they last signed in and
// when the account expires, with Extend, Suspend, Resume and Remove. It
// renders nothing on a deployment without external accounts.
export function ExternalUsersSection({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isError } = useExternalUsers();
  const [adding, setAdding] = useState(false);
  if (isError || !data) return null;
  return (
    <section aria-labelledby="external-users-heading" className="border-b">
      <div className="flex items-center justify-between px-5 py-3">
        <h3 id="external-users-heading" className="text-sm font-medium">
          External users
        </h3>
        {!isReadOnly && (
          <Button type="button" size="sm" variant={adding ? "outline" : "default"} onClick={() => setAdding((v) => !v)}>
            {adding ? <ChevronUp /> : <Plus />}
            {adding ? "Cancel" : "Add external user"}
          </Button>
        )}
      </div>
      {adding && <AddExternalUserForm onDone={() => setAdding(false)} />}
      {data.users.length === 0 ? (
        <p className="px-5 pb-4 text-xs text-muted-foreground">
          None yet. An external user signs in with a link emailed to them and sees only what is shared with them.
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/30 text-xs text-muted-foreground hover:bg-muted/30">
              <TableHead className="px-5">Email</TableHead>
              <TableHead className="px-5">Name</TableHead>
              <TableHead className="px-5">Persona</TableHead>
              <TableHead className="px-5">Shared items</TableHead>
              <TableHead className="px-5">Last sign-in</TableHead>
              <TableHead className="px-5">Expires</TableHead>
              {!isReadOnly && <TableHead className="px-5">Actions</TableHead>}
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.users.map((u) => (
              <ExternalUserRow key={u.id} user={u} isReadOnly={isReadOnly} />
            ))}
          </TableBody>
        </Table>
      )}
    </section>
  );
}

const STATUS_LABEL: Record<ExternalUser["status"], string> = {
  active: "",
  suspended: "Suspended",
  expired: "Expired",
  converted: "Converted",
};

function formatDate(iso?: string): string {
  return iso ? new Date(iso).toLocaleDateString() : "";
}

function ExternalUserRow({ user, isReadOnly }: { user: ExternalUser; isReadOnly: boolean }) {
  return (
    <TableRow>
      <TableCell className="px-5">
        {user.email}
        <StatusBadge user={user} />
      </TableCell>
      <TableCell className="px-5">{user.name}</TableCell>
      <TableCell className="px-5">{user.persona}</TableCell>
      <TableCell className="px-5">{user.shared_items}</TableCell>
      <TableCell className="px-5">{formatDate(user.last_sign_in_at)}</TableCell>
      <TableCell className="px-5">{formatDate(user.expires_at)}</TableCell>
      {!isReadOnly && (
        <TableCell className="px-5">
          <ExternalUserActions user={user} />
        </TableCell>
      )}
    </TableRow>
  );
}

// StatusBadge marks an account that is not active; a converted one carries
// the date it converted.
function StatusBadge({ user }: { user: ExternalUser }) {
  const status = STATUS_LABEL[user.status];
  if (!status) return null;
  const converted = user.status === "converted" && user.converted_at ? ` ${formatDate(user.converted_at)}` : "";
  return (
    <Badge variant="outline" className="ml-2">
      {status}
      {converted}
    </Badge>
  );
}

// ExternalUserActions is Extend, Suspend or Resume, and Remove after a
// confirmation. A converted account is closed, so only Remove is left.
function ExternalUserActions({ user }: { user: ExternalUser }) {
  const action = useExternalUserAction();
  const [confirmRemove, setConfirmRemove] = useState(false);
  const closed = user.status === "converted";
  const suspended = user.status === "suspended";
  return (
    <>
      {action.error && <p className="text-xs text-destructive">{action.error.message}</p>}
      <div className="flex flex-wrap items-center gap-1.5">
        {!closed && <ExtendControl user={user} />}
        {!closed && !suspended && (
          <Button size="xs" variant="outline" onClick={() => action.mutate({ id: user.id, action: "suspend" })}>
            Suspend
          </Button>
        )}
        {suspended && (
          <Button size="xs" variant="outline" onClick={() => action.mutate({ id: user.id, action: "resume" })}>
            Resume
          </Button>
        )}
        {confirmRemove ? (
          <Button size="xs" variant="destructive" onClick={() => action.mutate({ id: user.id, action: "remove" })}>
            Remove, and revoke what is shared with them
          </Button>
        ) : (
          <Button size="xs" variant="outline" className="text-destructive" onClick={() => setConfirmRemove(true)}>
            Remove
          </Button>
        )}
      </div>
    </>
  );
}

// ExtendControl is Extend, then the new expiry and Save; an empty date
// removes the expiry.
function ExtendControl({ user }: { user: ExternalUser }) {
  const action = useExternalUserAction();
  const [extending, setExtending] = useState(false);
  const [until, setUntil] = useState(user.expires_at?.slice(0, 10) ?? "");
  if (!extending) {
    return (
      <Button size="xs" variant="outline" onClick={() => setExtending(true)}>
        Extend
      </Button>
    );
  }
  return (
    <>
      <Input
        type="date"
        aria-label={`New expiry for ${user.email}`}
        className="h-7 w-36"
        value={until}
        onChange={(e) => setUntil(e.target.value)}
      />
      <Button
        size="xs"
        onClick={() =>
          action.mutate(
            { id: user.id, action: "extend", expiresAt: endOfDay(until) ?? null },
            { onSuccess: () => setExtending(false) },
          )
        }
      >
        Save
      </Button>
    </>
  );
}

// endOfDay is the last second of a date the date input holds, local time, or
// undefined for no date.
function endOfDay(date: string): string | undefined {
  return date ? new Date(`${date}T23:59:59`).toISOString() : undefined;
}

// useExternalPersonas is the personas an external account may be given.
function useExternalPersonas() {
  const { data } = usePersonas();
  return (data?.personas ?? []).filter((p) => p.external);
}

// AddExternalUserForm creates an account and sends its invitation.
function AddExternalUserForm({ onDone }: { onDone: () => void }) {
  const ids = useId();
  const create = useCreateExternalUser();
  const external = useExternalPersonas();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [persona, setPersona] = useState("");
  const [expires, setExpires] = useState("");
  const chosen = persona || external[0]?.name || "";

  const submit = () =>
    create.mutate(
      {
        email: email.trim(),
        name: name.trim(),
        persona: chosen,
        expires_at: endOfDay(expires),
      },
      { onSuccess: onDone },
    );

  return (
    <div className="space-y-3 bg-muted/10 px-5 py-4">
      {create.error && (
        <Alert variant="destructive">
          <AlertDescription>{create.error.message}</AlertDescription>
        </Alert>
      )}
      {external.length === 0 && (
        <Alert>
          <AlertDescription>
            No persona is external yet. Mark one External on its Portal tab under Admin, then Personas.
          </AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-4 gap-3">
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-email`} className="text-xs">
            Email
          </Label>
          <Input id={`${ids}-email`} type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-name`} className="text-xs">
            Name
          </Label>
          <Input id={`${ids}-name`} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-persona`} className="text-xs">
            Persona
          </Label>
          <select
            id={`${ids}-persona`}
            className="h-9 w-full rounded-md border bg-background px-2 text-sm"
            value={chosen}
            onChange={(e) => setPersona(e.target.value)}
          >
            {external.map((p) => (
              <option key={p.name} value={p.name}>
                {p.display_name || p.name}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-expires`} className="text-xs">
            Expires (optional)
          </Label>
          <Input id={`${ids}-expires`} type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />
        </div>
      </div>
      <Button size="sm" onClick={submit} disabled={!email.trim() || !chosen || create.isPending}>
        {create.isPending ? "Adding..." : "Add external user"}
      </Button>
    </div>
  );
}
