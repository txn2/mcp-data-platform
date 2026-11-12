import { useState } from "react";
import { useSetDomainAccess, type Site, type SiteDomain } from "@/api/portal/hooks/sites";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { Failure } from "./SiteParts";
import { errorText } from "./siteFormat";

// A domain's access (#2619): making one public names it and waits for the
// owner or an administrator to confirm, or files a request where its
// environment requires approval; making one private is anyone's who manages
// the site.

// ConfirmPublic names the one address that would open to anyone before
// anything changes (#2619). The owner or an administrator confirms; on an
// environment that requires approval, someone who is not an administrator asks.
export function ConfirmPublic({
  site,
  hostname,
  environment,
  asks,
  busy,
  failure,
  onConfirm,
  onClose,
  adding = false,
}: {
  site: Site;
  hostname: string;
  environment: string;
  asks: boolean;
  busy: boolean;
  failure: string | null;
  onConfirm: (reason?: string) => void;
  onClose: () => void;
  adding?: boolean;
}) {
  const [typed, setTyped] = useState("");
  const [reason, setReason] = useState("");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Make {hostname} public?</DialogTitle>
          <DialogDescription>
            {site.name} / {environment}
          </DialogDescription>
        </DialogHeader>
        {site.permissions.own ? (
          <div className="space-y-3">
            <div className="space-y-1">
              <p className="text-sm font-medium">Anyone on the internet can open this, without signing in</p>
              <p className="break-all font-mono text-sm">{hostname}</p>
            </div>
            <ul className="list-disc space-y-1 pl-5 text-sm">
              <li>The platform stops asking for a sign-in at {hostname}. If the site needs one there, it has to provide its own.</li>
              <li>Every release promoted to {environment} is public there. Its addresses on the pages domain keep asking for a sign-in.</li>
            </ul>
            {asks ? (
              <>
                <div className="space-y-1">
                  <Label htmlFor="public-reason">Why make it public</Label>
                  <Textarea id="public-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
                </div>
                <p className="text-xs text-muted-foreground">
                  {environment} requires approval. This sends a request to Approvals, and {hostname} {adding ? "is added private and stays" : "stays"} private until it is approved and you finish.
                </p>
              </>
            ) : (
              <div className="space-y-1">
                <Label htmlFor="public-confirm">Type {hostname} to confirm</Label>
                <Input id="public-confirm" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
              </div>
            )}
            <Failure text={failure} />
          </div>
        ) : (
          <Alert variant="destructive">
            <AlertTitle>Only the owner or an administrator can make a domain public</AlertTitle>
            <AlertDescription>
              {site.owner_email} owns {site.name}.
            </AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {site.permissions.own ? "Cancel" : "Close"}
          </Button>
          {site.permissions.own &&
            (asks ? (
              <Button onClick={() => onConfirm(reason.trim())} disabled={busy}>
                Ask for approval
              </Button>
            ) : (
              <Button variant="destructive" onClick={() => onConfirm()} disabled={typed.trim() !== hostname || busy}>
                {adding ? "Add public domain" : "Make public"}
              </Button>
            ))}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// MakeDomainPublicDialog opens one domain to anyone, or asks for it in
// Approvals where its environment requires approval.
export function MakeDomainPublicDialog({ site, hostname, environment, onClose }: { site: Site; hostname: string; environment: string; onClose: () => void }) {
  const set = useSetDomainAccess(site.name);
  const isAdmin = useAuthStore((st) => st.isAdmin());
  const [failure, setFailure] = useState<string | null>(null);
  const gated = !!site.environments.find((e) => e.name === environment)?.requires_approval;
  const confirm = (reason?: string) => {
    setFailure(null);
    set.mutate({ hostname, public: true, ...(reason ? { reason } : {}) }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The domain could not be made public")) });
  };
  return <ConfirmPublic site={site} hostname={hostname} environment={environment} asks={gated && !isAdmin} busy={set.isPending} failure={failure} onConfirm={confirm} onClose={onClose} />;
}

// MakeDomainPrivateDialog closes a public domain: it asks for a sign-in from
// the next request. Anyone who manages the site can.
export function MakeDomainPrivateDialog({ site, domain, onClose }: { site: Site; domain: SiteDomain; onClose: () => void }) {
  const set = useSetDomainAccess(site.name);
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    set.mutate({ hostname: domain.hostname, public: false }, { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The domain could not be made private")) });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Make {domain.hostname} private?</DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          From the next request it asks for a sign-in and opens for whoever {domain.environment} is shared with, the people who manage {site.name} and administrators. Making it public again is the owner's or an administrator's.
        </p>
        <Failure text={failure} />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={set.isPending}>
            Make private
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
