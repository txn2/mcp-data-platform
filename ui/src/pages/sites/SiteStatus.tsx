import { useState } from "react";
import { DEV_ENVIRONMENT, statusOf, useSetSiteEnvironment, type Site, type SiteEnvironment } from "@/api/portal/hooks/sites";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { Failure } from "./SiteParts";
import { errorText, when } from "./siteFormat";

// An environment's status (#2631, #2632): Online serves its release, Offline
// answers every address with a message, Redirecting sends every address to
// another https site. dev is always online. The release is untouched, so
// promoting and rolling back keep working meanwhile.

/** The longest an offline message may be, as the server holds it. */
export const MAX_MESSAGE = 1000;

/** The badge an environment's card and the Sites list show; none on dev. */
export function StatusBadge({ env }: { env: SiteEnvironment }) {
  if (env.name === DEV_ENVIRONMENT) return null;
  switch (statusOf(env)) {
    case "offline":
      return <Badge variant="danger">Offline</Badge>;
    case "redirect":
      return <Badge variant="warning">Redirecting</Badge>;
    default:
      return <Badge variant="success">Online</Badge>;
  }
}

/** A request path cleaned as the gateway cleans it: no empty, "." or ".." segment. */
function cleanPath(rawPath: string): string {
  const segments: string[] = [];
  for (const seg of rawPath.split("/")) {
    if (seg === ".." ) segments.pop();
    else if (seg !== "" && seg !== ".") segments.push(seg);
  }
  const clean = "/" + segments.join("/");
  return rawPath.endsWith("/") && clean !== "/" ? `${clean}/` : clean;
}

/** Where a redirected request lands, as the gateway writes it. */
export function redirectedTo(target: string, keepPath: boolean, path: string): string {
  let u: URL;
  try {
    u = new URL(target);
  } catch {
    return "";
  }
  if (!keepPath) return u.toString();
  const [rawPath = "", query = ""] = path.split("?");
  u.pathname = u.pathname.replace(/\/$/, "") + cleanPath(rawPath);
  u.search = query ? `?${query}` : "";
  u.hash = "";
  return u.toString();
}

/** Who changed an environment's status, and when, as one sentence. */
function sinceLine(env: SiteEnvironment): string {
  if (!env.status_changed_at) return "";
  return `Since ${when(env.status_changed_at)}${env.status_changed_by ? ` by ${env.status_changed_by}` : ""}.`;
}

function OfflineText({ env }: { env: SiteEnvironment }) {
  return (
    <>
      <p className="font-medium">Every address answers 503 with this message</p>
      <p className="whitespace-pre-line">{env.message || "This site is offline for now."}</p>
      {env.back_at && <p className="text-xs text-muted-foreground">Expected back {when(env.back_at)}.</p>}
    </>
  );
}

function RedirectText({ env }: { env: SiteEnvironment }) {
  return (
    <>
      <p className="font-medium">Every address sends visitors to</p>
      <p className="break-all font-mono text-[13px]">{env.redirect_to}</p>
      <p className="text-xs text-muted-foreground">{env.keep_path ? "The path and query are kept." : "Every path goes to this address as written."}</p>
    </>
  );
}

const STATUS_TONE = {
  offline: "border-red-300 bg-red-50 dark:border-red-500/30 dark:bg-red-500/10",
  redirect: "border-amber-300 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10",
} as const;

/** What an offline or redirecting environment shows on its card, with Bring online. */
export function StatusBlock({ site, env, canEdit }: { site: Site; env: SiteEnvironment; canEdit: boolean }) {
  const status = statusOf(env);
  const set = useSetSiteEnvironment(site.name);
  const [failure, setFailure] = useState<string | null>(null);
  if (status === "online") return null;
  const online = () => set.mutate({ name: env.name, status: "online" }, { onError: (e) => setFailure(errorText(e, "The environment could not be brought online")) });
  const since = sinceLine(env);
  return (
    <div className={`space-y-1 rounded-md border p-3 text-sm ${STATUS_TONE[status]}`} data-testid={`status-${env.name}`}>
      {status === "offline" ? <OfflineText env={env} /> : <RedirectText env={env} />}
      {since && <p className="text-xs text-muted-foreground">{since}</p>}
      {canEdit && (
        <Button size="sm" className="mt-1" onClick={online} disabled={set.isPending}>
          Bring online
        </Button>
      )}
      <Failure text={failure} />
    </div>
  );
}

/** Today in the local zone, as a datetime-local value, plus an hour. */
function inAnHour(): string {
  const d = new Date(Date.now() + 3600_000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** Takes an environment offline with a message and an optional expected-back time (#2631). */
export function TakeOfflineDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const [message, setMessage] = useState("");
  const [back, setBack] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const tooLong = [...message].length > MAX_MESSAGE;
  const submit = () => {
    setFailure(null);
    const body = { name: env.name, status: "offline" as const, message: message.trim(), ...(back ? { back_at: new Date(back).toISOString() } : {}) };
    set.mutate(body, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The environment could not be taken offline")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Take {env.name} offline</DialogTitle>
          <DialogDescription>
            Every address of {env.name} answers 503 with your message, after the usual sign-in. The release stays where it is, and Bring online serves it again.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="offline-message">Message</Label>
            <Textarea id="offline-message" value={message} onChange={(e) => setMessage(e.target.value)} placeholder="This site is offline for now." aria-invalid={tooLong} />
            <p className={`text-xs ${tooLong ? "text-destructive" : "text-muted-foreground"}`}>
              {[...message].length} / {MAX_MESSAGE}. Plain text: no links or formatting.
            </p>
          </div>
          <div className="space-y-1">
            <Label htmlFor="offline-back">Expected back</Label>
            <div className="flex gap-2">
              <Input id="offline-back" type="datetime-local" value={back} onChange={(e) => setBack(e.target.value)} />
              {!back && (
                <Button type="button" variant="outline" size="sm" onClick={() => setBack(inAnHour())}>
                  In an hour
                </Button>
              )}
            </div>
            <p className="text-xs text-muted-foreground">Only what visitors are told. {env.name} stays offline until you bring it online.</p>
          </div>
          <Failure text={failure} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={submit} disabled={set.isPending || tooLong}>
            Take offline
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const PREVIEW_PATHS = ["/pricing/?a=1", "//evil.example/x"];

/** Redirects an environment to another website for a while (#2632). */
export function RedirectDialog({ site, env, onClose }: { site: Site; env: SiteEnvironment; onClose: () => void }) {
  const set = useSetSiteEnvironment(site.name);
  const [target, setTarget] = useState(env.redirect_to ?? "https://");
  const [keep, setKeep] = useState(env.keep_path ?? false);
  const [reason, setReason] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [waiting, setWaiting] = useState(false);
  const admin = useAuthStore((st) => st.user?.is_admin === true);
  const asks = env.requires_approval && !admin;
  const submit = () => {
    setFailure(null);
    set.mutate(
      { name: env.name, status: "redirect", redirect_to: target.trim(), keep_path: keep, ...(reason ? { reason } : {}) },
      {
        onSuccess: (r) => (r.status === "awaiting_approval" ? setWaiting(true) : onClose()),
        onError: (e) => setFailure(errorText(e, "The environment could not be redirected")),
      },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Redirect {env.name}</DialogTitle>
          <DialogDescription>Every address of {env.name} sends visitors to another website until you bring it online. Browsers keep nothing of it.</DialogDescription>
        </DialogHeader>
        {waiting ? (
          <p className="text-sm" data-testid="redirect-waiting">
            {env.name} requires approval, so the redirect waits in Approvals. {env.name} keeps answering as it does; once it is approved, redirect it again to apply it.
          </p>
        ) : (
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="redirect-target">Target</Label>
              <Input id="redirect-target" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="https://status.example.com" />
              <p className="text-xs text-muted-foreground">An https:// address on another website.</p>
            </div>
            <label className="flex items-start gap-2">
              <input type="checkbox" className="mt-1" checked={keep} onChange={(e) => setKeep(e.target.checked)} />
              <span>
                <span className="block font-medium">Keep the path</span>
                <span className="block text-xs text-muted-foreground">Add the visitor's path and query to the target.</span>
              </span>
            </label>
            <div className="space-y-1 rounded-md bg-muted p-2 text-xs" data-testid="redirect-preview">
              {PREVIEW_PATHS.map((p) => (
                <p key={p} className="break-all font-mono">
                  {p} → {redirectedTo(target, keep, p) || "…"}
                </p>
              ))}
            </div>
            {asks && (
              <div className="space-y-1">
                <Label htmlFor="redirect-reason">Reason</Label>
                <Input id="redirect-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="For the approver" />
                <p className="text-xs text-muted-foreground">{env.name} requires approval, so this redirect waits in Approvals.</p>
              </div>
            )}
            <Failure text={failure} />
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {waiting ? "Close" : "Cancel"}
          </Button>
          {!waiting && (
            <Button onClick={submit} disabled={set.isPending}>
              {asks ? "Ask for approval" : "Redirect"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
