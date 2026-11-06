import { useState } from "react";
import {
  useApprovals,
  useOpenFormPreview,
  useRequestApproval,
  type ApprovalRequest,
} from "@/api/portal/hooks/approvals";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

// FormPublish is what a form's owner publishes it with (#2494): a preview of
// the form as /forms/<endpoint> renders it, with nothing bound and nothing
// stored, and the request that lets an administrator approve a version for
// people who are not signed in. Publishing an internal form is the binding
// itself; a public form serves the version approved.

/** approvedVersion is the highest version approved, 0 for none. */
export function approvedVersion(requests: ApprovalRequest[] | undefined): number {
  return (requests ?? []).filter((r) => r.status === "approved").reduce((v, r) => Math.max(v, r.version), 0);
}

export function FormPublish({ scriptId }: { scriptId: string }) {
  const approvals = useApprovals({ scriptId });
  const approved = approvedVersion(approvals.data);
  const pending = (approvals.data ?? []).filter((r) => r.status === "pending");
  const [requesting, setRequesting] = useState(false);
  return (
    <div className="space-y-3 rounded-md border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <PreviewButton scriptId={scriptId} label="Preview form" />
        <Button size="sm" variant="outline" onClick={() => setRequesting(true)} disabled={requesting}>
          Request public access
        </Button>
      </div>
      <p className="text-xs text-muted-foreground" data-testid="form-public-status">
        {publicStatus(approved, pending)}
      </p>
      {requesting && <RequestForm scriptId={scriptId} onDone={() => setRequesting(false)} />}
    </div>
  );
}

function publicStatus(approved: number, pending: ApprovalRequest[]): string {
  const waiting = pending.map((r) => `version ${r.version}`).join(", ");
  const parts = [
    approved > 0
      ? `Version ${approved} is approved for people who are not signed in.`
      : "No version is approved for people who are not signed in; signed-in people of this deployment can be served it now.",
  ];
  if (waiting) parts.push(`Waiting for an administrator: ${waiting}.`);
  return parts.join(" ");
}

// PreviewButton opens a preview in a new tab: of the latest saved version,
// of version when it is given, or of source when that is.
export function PreviewButton({
  scriptId,
  label,
  source,
  version,
  disabled,
}: {
  scriptId: string;
  label: string;
  source?: string;
  version?: number;
  disabled?: boolean;
}) {
  const open = useOpenFormPreview(scriptId);
  return (
    <>
      <Button
        size="sm"
        variant="outline"
        disabled={disabled || open.isPending}
        onClick={() =>
          open.mutate(source !== undefined ? { source } : version ? { version } : {}, {
            onSuccess: (p) => window.open(p.url, "_blank", "noopener,noreferrer"),
          })
        }
      >
        {open.isPending ? "Opening..." : label}
      </Button>
      {open.data && (
        <a className="text-xs underline" href={open.data.url} target="_blank" rel="noopener noreferrer">
          Open the preview
        </a>
      )}
      {open.error && <span className="text-xs text-destructive">{open.error.message}</span>}
    </>
  );
}

function RequestForm({ scriptId, onDone }: { scriptId: string; onDone: () => void }) {
  const request = useRequestApproval(scriptId);
  const [reason, setReason] = useState("");
  return (
    <div className="space-y-2">
      <Label htmlFor="approval-reason" className="text-xs text-muted-foreground">
        Why the form should be open to people who are not signed in
      </Label>
      <Textarea id="approval-reason" rows={3} value={reason} onChange={(e) => setReason(e.target.value)} />
      <p className="text-xs text-muted-foreground">
        This asks an administrator to approve the latest saved version. Each version saved after an approval is served
        to signed-in people at once, and to everyone else once it is approved too.
      </p>
      <div className="flex gap-2">
        <Button size="sm" disabled={request.isPending} onClick={() => request.mutate({ version: 0, reason: reason.trim() }, { onSuccess: onDone })}>
          Send request
        </Button>
        <Button size="sm" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>
      {request.error && (
        <Alert variant="destructive">
          <AlertDescription>{request.error.message}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
