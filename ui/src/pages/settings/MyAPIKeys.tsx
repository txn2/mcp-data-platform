import { useCallback, useId, useState } from "react";
import { Check, Copy, KeyRound, Trash2, X } from "lucide-react";
import {
  useCreateMyAPIKey,
  useMyAPIKeys,
  useRevokeMyAPIKey,
  type MyAPIKey,
  type MyAPIKeyCreated,
} from "@/api/portal/hooks";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ConfigSelect } from "./connections/fields";
import { SettingsCard } from "./panels";
import { ErrorBanner } from "./settingsChrome";
import { cn } from "@/lib/utils";

// EXPIRATION_OPTIONS are the lifetimes offered. "Never" is the default: a key
// somebody made for a desktop client should not stop working unannounced, and
// they can revoke it here the moment they want it gone.
const EXPIRATION_OPTIONS = [
  { label: "Never", value: "" },
  { label: "24 hours", value: "24h" },
  { label: "7 days", value: "168h" },
  { label: "30 days", value: "720h" },
  { label: "90 days", value: "2160h" },
  { label: "1 year", value: "8760h" },
];

function formatExpiration(expiresAt?: string): string {
  if (!expiresAt) return "Never";
  return new Date(expiresAt).toLocaleDateString();
}

// MyAPIKeys is a person's own API keys (#1759). A key issued here authenticates
// as them: same identity, same roles, same work visible, whether they arrive
// through a client that speaks OAuth or one that can only send a bearer token.
export function MyAPIKeys() {
  const { data, isLoading, error: loadError, refetch } = useMyAPIKeys();
  const revoke = useRevokeMyAPIKey();
  const [created, setCreated] = useState<MyAPIKeyCreated | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const handleRevoke = useCallback(
    (name: string) => {
      setActionError(null);
      revoke.mutate(name, {
        onSuccess: () => setConfirming(null),
        onError: (err) => {
          setConfirming(null);
          setActionError(err instanceof Error ? err.message : "Failed to revoke the key");
        },
      });
    },
    [revoke],
  );

  const keys = data?.keys ?? [];

  return (
    <SettingsCard
      icon={KeyRound}
      title="API Keys"
      description="Keys that authenticate as you, for clients that cannot sign in"
      notices={
        loadError && (
          <ErrorBanner
            message="Failed to load your API keys. The server may be unavailable."
            onRetry={() => void refetch()}
          />
        )
      }
      feedback={actionError && <ErrorBanner message={actionError} />}
    >
      <div className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Some clients cannot sign in through your identity provider and can only
          send a bearer token. A key issued here authenticates as you, with the
          roles you hold, so what you do through such a client is yours and is
          here when you come back to the portal.
        </p>

        {created && <CreatedKeyBanner created={created} onDismiss={() => setCreated(null)} />}

        <NewKeyForm onCreated={setCreated} onError={setActionError} />

        {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}

        {!isLoading && keys.length === 0 && (
          <p className="text-sm italic text-muted-foreground">
            You have no API keys.
          </p>
        )}

        {keys.length > 0 && (
          <KeyList
            keys={keys}
            confirming={confirming}
            revoking={revoke.isPending}
            onRequestRevoke={setConfirming}
            onCancel={() => setConfirming(null)}
            onConfirm={handleRevoke}
          />
        )}
      </div>
    </SettingsCard>
  );
}

// NewKeyForm issues a key. It asks for a name and a lifetime and nothing else:
// the key is yours, carries what you carry, and there is nothing to choose
// about who it is or what it may reach.
function NewKeyForm({
  onCreated,
  onError,
}: {
  onCreated: (created: MyAPIKeyCreated) => void;
  onError: (message: string | null) => void;
}) {
  const create = useCreateMyAPIKey();
  const ids = useId();
  const [name, setName] = useState("");
  const [expiresIn, setExpiresIn] = useState("");

  const submit = useCallback(() => {
    const trimmed = name.trim();
    if (!trimmed) return;
    onError(null);
    create.mutate(
      { name: trimmed, expires_in: expiresIn || undefined },
      {
        onSuccess: (resp) => {
          onCreated(resp);
          setName("");
          setExpiresIn("");
        },
        onError: (err) => {
          onError(err instanceof Error ? err.message : "Failed to create the key");
        },
      },
    );
  }, [name, expiresIn, create, onCreated, onError]);

  return (
    <div className="flex flex-wrap items-end gap-3 rounded-md border bg-muted/20 p-3">
      <div className="min-w-48 flex-1 space-y-1.5">
        <Label htmlFor={`${ids}-name`} className="text-xs">
          Name
        </Label>
        <Input
          id={`${ids}-name`}
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
          placeholder="What will use this key? e.g. ChatGPT"
        />
      </div>
      <div className="w-36">
        <ConfigSelect
          label="Expiration"
          value={expiresIn}
          onChange={setExpiresIn}
          options={EXPIRATION_OPTIONS}
        />
      </div>
      <Button type="button" size="sm" onClick={submit} disabled={create.isPending || !name.trim()}>
        <KeyRound />
        {create.isPending ? "Creating..." : "Create key"}
      </Button>
    </div>
  );
}

// CreatedKeyBanner shows the key value, which exists here and nowhere else.
function CreatedKeyBanner({
  created,
  onDismiss,
}: {
  created: MyAPIKeyCreated;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(() => {
    void navigator.clipboard.writeText(created.key).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }, [created.key]);

  return (
    <div className="space-y-2 rounded-md border border-warning/50 bg-warning/10 p-3">
      <div className="flex items-start justify-between gap-2">
        <div className="space-y-1">
          <p className="text-sm font-medium">Key created: {created.name}</p>
          <p className="text-xs text-muted-foreground">{created.warning}</p>
        </div>
        <Button type="button" variant="ghost" size="sm" onClick={onDismiss} aria-label="Dismiss">
          <X />
        </Button>
      </div>
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded bg-background px-2 py-1.5 font-mono text-xs">
          {created.key}
        </code>
        <Button type="button" variant="outline" size="sm" onClick={copy}>
          {copied ? <Check /> : <Copy />}
          {copied ? "Copied" : "Copy"}
        </Button>
      </div>
    </div>
  );
}

// KeyList is the keys a person holds, each revocable here.
function KeyList({
  keys,
  confirming,
  revoking,
  onRequestRevoke,
  onCancel,
  onConfirm,
}: {
  keys: MyAPIKey[];
  confirming: string | null;
  revoking: boolean;
  onRequestRevoke: (name: string) => void;
  onCancel: () => void;
  onConfirm: (name: string) => void;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="bg-muted/30 text-xs text-muted-foreground hover:bg-muted/30">
          <TableHead className="w-full">Name</TableHead>
          <TableHead>Roles</TableHead>
          <TableHead>Expiration</TableHead>
          <TableHead className="w-20">Actions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {keys.map((k) => (
          <TableRow key={k.name} className={cn(k.expired && "opacity-50")}>
            <TableCell className="py-3 font-medium">
              <span className={cn(k.expired && "line-through")}>{k.name}</span>
              {k.expired && (
                <Badge variant="danger" className="ml-2">
                  Expired
                </Badge>
              )}
            </TableCell>
            <TableCell className="py-3">
              <div className="flex flex-wrap gap-1">
                {k.roles.length === 0 ? (
                  <span className="text-xs italic text-muted-foreground opacity-50">None</span>
                ) : (
                  k.roles.map((r) => (
                    <Badge key={r} variant="outline">
                      {r}
                    </Badge>
                  ))
                )}
              </div>
            </TableCell>
            <TableCell className="py-3 text-muted-foreground">
              {formatExpiration(k.expires_at)}
            </TableCell>
            <TableCell className="py-3">
              {confirming === k.name ? (
                <div className="flex items-center gap-1">
                  <Button
                    type="button"
                    variant="destructive"
                    size="sm"
                    disabled={revoking}
                    onClick={() => onConfirm(k.name)}
                  >
                    {revoking ? "Revoking..." : "Revoke"}
                  </Button>
                  <Button type="button" variant="ghost" size="sm" onClick={onCancel} aria-label="Cancel">
                    <X />
                  </Button>
                </div>
              ) : (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={() => onRequestRevoke(k.name)}
                  aria-label={`Revoke ${k.name}`}
                >
                  <Trash2 />
                </Button>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
