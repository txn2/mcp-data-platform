import { useState } from "react";
import { Check, Copy, Loader2, X } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ModalShell } from "@/components/ModalShell";
import {
  useCreateUploadLink,
  useRevokeUploadLink,
  useUploadLinks,
  type IssuedUploadLink,
  type NewUploadLink,
  type UploadLink,
} from "@/api/resources/links";
import { displayPath, type ResourceRoot } from "../scopes";

// Upload links on the Resources page (#2844): a person makes a link for a
// folder to give an external service, sees it once, and later sees the links
// they made that can still take a file and revokes one.

const MB = 1024 * 1024;

/** How long a new link lasts, the choices the dialog offers. */
const lifetimes = [
  { seconds: 900, label: "15 minutes" },
  { seconds: 3600, label: "1 hour" },
  { seconds: 21600, label: "6 hours" },
  { seconds: 86400, label: "24 hours" },
];

/** "in 5 hours", "in 12 minutes": how long until an expiry. */
export function untilText(iso: string, now = Date.now()): string {
  const minutes = Math.max(0, Math.round((new Date(iso).getTime() - now) / 60000));
  if (minutes < 60) return `in ${minutes} minute${minutes === 1 ? "" : "s"}`;
  const hours = Math.round(minutes / 60);
  return `in ${hours} hour${hours === 1 ? "" : "s"}`;
}

/** Where a listed link files, written as the page writes a location. */
export function linkFolder(l: Pick<UploadLink, "scope" | "scope_id" | "path" | "filename">): string {
  const library = l.scope === "global" ? "Global" : l.scope === "persona" ? (l.scope_id ?? "Persona") : "My Resources";
  return "/" + [library, ...l.path.split("/").filter(Boolean), ...(l.filename ? [l.filename] : [])].join("/");
}

function Header({ title, onClose }: { title: string; onClose: () => void }) {
  return (
    <div className="flex items-center justify-between border-b p-4">
      <h2 className="text-lg font-semibold">{title}</h2>
      <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
        <X />
      </Button>
    </div>
  );
}

/** What the New upload link form holds. */
interface LinkForm {
  folder: string;
  contentType: string;
  sizeMB: string;
  files: string;
  ttl: number;
  named: boolean;
  filename: string;
  ifExists: "fail" | "replace";
}

const blankForm: LinkForm = { folder: "", contentType: "", sizeMB: "20", files: "1", ttl: 21600, named: true, filename: "", ifExists: "fail" };

/** What keeps the form from being sent, or "" when nothing does. */
export function formProblem(f: LinkForm): string {
  const count = Number.parseInt(f.files, 10);
  if (!f.folder.trim()) return "Name a folder: a file is filed in a folder, for example renders.";
  if (!f.contentType.trim()) return "Name the type of file the service sends, for example image/png.";
  if (!(Number.parseFloat(f.sizeMB) > 0)) return "Largest file is a size in MB.";
  if (!(count >= 1 && count <= 100)) return "Files is 1 to 100.";
  if (count === 1 && !f.named && !f.filename.trim()) return "Name the file, or let the service name it.";
  return "";
}

/** The link the form asks for, in the folder of root. */
export function linkRequest(f: LinkForm, root: ResourceRoot): NewUploadLink {
  const count = Number.parseInt(f.files, 10);
  return {
    scope: root.target.scope,
    scope_id: root.target.scope_id || undefined,
    path: f.folder.trim().replace(/^\/+|\/+$/g, ""),
    filename: count === 1 && !f.named ? f.filename.trim() : undefined,
    content_type: f.contentType.trim(),
    size_bytes: Math.round(Number.parseFloat(f.sizeMB) * MB),
    max_files: count,
    ttl_seconds: f.ttl,
    if_exists: f.ifExists,
  };
}

/** New upload link: the form, then the link shown once. */
export function NewUploadLinkDialog({ root, folder, onClose }: { root: ResourceRoot; folder: string; onClose: () => void }) {
  const create = useCreateUploadLink();
  const [form, setForm] = useState<LinkForm>({ ...blankForm, folder });
  const set = (patch: Partial<LinkForm>) => setForm((f) => ({ ...f, ...patch }));
  const invalid = formProblem(form);

  if (create.data) {
    return (
      <LinkReady root={root} folder={linkRequest(form, root).path} issued={create.data} contentType={form.contentType.trim()} onClose={onClose} />
    );
  }
  return (
    <ModalShell
      onClose={onClose}
      label="New upload link"
      busy={create.isPending}
      bodyClass="space-y-3.5 p-4"
      header={<Header title="New upload link" onClose={onClose} />}
      footer={
        <div className="flex justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={() => create.mutate(linkRequest(form, root))} disabled={create.isPending || invalid !== ""}>
            {create.isPending && <Loader2 className="animate-spin" />}
            {create.isPending ? "Making..." : "Make link"}
          </Button>
        </div>
      }
    >
      <p className="text-sm text-muted-foreground">
        A service PUTs its files to this link, and they're filed here as you. Anyone with the link can upload until it expires,
        so give it only to the service.
      </p>
      <Field id="ul-folder" label={`Folder in ${displayPath(root, "")}`}>
        <Input id="ul-folder" value={form.folder} placeholder="renders" onChange={(e) => set({ folder: e.target.value })} />
      </Field>
      <LinkFields form={form} set={set} />
      {invalid && (form.contentType !== "" || !form.folder.trim()) && <p className="text-xs text-destructive">{invalid}</p>}
      {create.error && (
        <Alert variant="destructive">
          <AlertDescription>{(create.error as Error).message}</AlertDescription>
        </Alert>
      )}
    </ModalShell>
  );
}

/** The form's fields: type, size, count, lifetime, naming and what an existing name does. */
function LinkFields({ form, set }: { form: LinkForm; set: (patch: Partial<LinkForm>) => void }) {
  const one = Number.parseInt(form.files, 10) === 1;
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <Field id="ul-type" label="File type">
          <Input id="ul-type" value={form.contentType} placeholder="image/png" onChange={(e) => set({ contentType: e.target.value })} />
        </Field>
        <Field id="ul-size" label="Largest file (MB)">
          <Input id="ul-size" inputMode="decimal" value={form.sizeMB} onChange={(e) => set({ sizeMB: e.target.value })} />
        </Field>
        <Field id="ul-files" label="Files">
          <Input id="ul-files" inputMode="numeric" value={form.files} onChange={(e) => set({ files: e.target.value })} />
        </Field>
        <Field id="ul-ttl" label="Expires after">
          <select
            id="ul-ttl"
            className="h-9 w-full rounded-md border bg-card px-2 text-sm"
            value={form.ttl}
            onChange={(e) => set({ ttl: Number(e.target.value) })}
          >
            {lifetimes.map((l) => (
              <option key={l.seconds} value={l.seconds}>
                {l.label}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <fieldset className="space-y-1.5">
        <legend className="mb-1.5 text-xs text-muted-foreground">File names</legend>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" name="ul-names" checked={form.named || !one} onChange={() => set({ named: true })} />
          The service names each file in the link
        </label>
        <label className={`flex items-center gap-2 text-sm ${one ? "" : "text-muted-foreground"}`}>
          <input type="radio" name="ul-names" checked={one && !form.named} disabled={!one} onChange={() => set({ named: false })} />
          One file, named:
          {one && !form.named ? (
            <Input
              aria-label="File name"
              className="h-8 flex-1"
              value={form.filename}
              placeholder="report.pdf"
              onChange={(e) => set({ filename: e.target.value })}
            />
          ) : (
            <span className="text-xs">{one ? "" : "(for a single file)"}</span>
          )}
        </label>
      </fieldset>
      <Field id="ul-exists" label="If a file of that name is already here">
        <select
          id="ul-exists"
          className="h-9 w-full rounded-md border bg-card px-2 text-sm"
          value={form.ifExists}
          onChange={(e) => set({ ifExists: e.target.value as "fail" | "replace" })}
        >
          <option value="fail">Refuse the upload</option>
          <option value="replace">Save it as a new version</option>
        </select>
      </Field>
    </>
  );
}

function Field({ id, label, children }: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs font-normal text-muted-foreground">
        {label}
      </Label>
      {children}
    </div>
  );
}

/** The link as it is made, the one time it is shown. */
function LinkReady({
  root,
  folder,
  issued,
  contentType,
  onClose,
}: {
  root: ResourceRoot;
  folder: string;
  issued: IssuedUploadLink;
  contentType: string;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(issued.upload_url).catch(() => undefined);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  const short = issued.upload_url.replace(/\/([^/]{4})[^/]+([^/]{6})$/, "/$1…$2");
  const example = issued.name_in_url
    ? `curl --fail -T file.ext -H 'Content-Type: ${contentType}' '${short}/file.ext'`
    : `curl --fail -T file.ext -H 'Content-Type: ${contentType}' '${short}'`;
  const expires = new Date(issued.expires_at);
  return (
    <ModalShell
      onClose={onClose}
      label="Upload link ready"
      bodyClass="space-y-3.5 p-4"
      header={<Header title="Upload link ready" onClose={onClose} />}
      footer={
        <div className="flex justify-end gap-2 border-t p-4">
          <Button onClick={onClose}>Done</Button>
        </div>
      }
    >
      <div className="space-y-1.5">
        <span className="text-xs text-muted-foreground">Link</span>
        <div className="flex items-start gap-2">
          <code data-testid="upload-link-url" className="min-w-0 flex-1 rounded-md border bg-background px-2.5 py-2 font-mono text-xs break-all">
            {issued.upload_url}
          </code>
          <Button variant="outline" size="sm" onClick={copy}>
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy"}
          </Button>
        </div>
      </div>
      <Alert variant="warning">
        <AlertDescription>This is the only time the link is shown. If you lose it, revoke it under Upload links and make another.</AlertDescription>
      </Alert>
      <dl className="grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-sm">
        <dt className="text-muted-foreground">Folder</dt>
        <dd>{displayPath(root, folder)}</dd>
        <dt className="text-muted-foreground">Takes</dt>
        <dd>
          {issued.max_files} file{issued.max_files === 1 ? "" : "s"}, {contentType}, up to {Math.round((issued.max_bytes / MB) * 10) / 10} MB
          {issued.max_files === 1 ? "" : " each"}
        </dd>
        <dt className="text-muted-foreground">File names</dt>
        <dd>{issued.name_in_url ? <>After the link: <span className="font-mono text-xs">…/&lt;filename&gt;</span></> : "Fixed by the link"}</dd>
        <dt className="text-muted-foreground">Expires</dt>
        <dd>
          {expires.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })} (
          {untilText(issued.expires_at)})
        </dd>
      </dl>
      <div className="space-y-1.5">
        <span className="text-xs text-muted-foreground">How the service sends a file</span>
        <code className="block rounded-md border bg-background px-2.5 py-2 font-mono text-xs break-all">{example}</code>
        <span className="block text-xs text-muted-foreground">A failed upload doesn't use up a file; the service can send it again.</span>
      </div>
    </ModalShell>
  );
}

/** Upload links: the caller's links that can still take a file. */
export function UploadLinksDialog({ onClose, onNew }: { onClose: () => void; onNew?: () => void }) {
  const links = useUploadLinks();
  const revoke = useRevokeUploadLink();
  const [confirming, setConfirming] = useState<string | null>(null);
  const [failed, setFailed] = useState("");
  const [revoked, setRevoked] = useState(false);
  const rows = links.data ?? [];
  return (
    <ModalShell
      onClose={onClose}
      label="Upload links"
      width="max-w-3xl"
      bodyClass="space-y-3 p-4"
      header={<Header title="Upload links" onClose={onClose} />}
      footer={
        <div className="flex justify-between gap-2 border-t p-4">
          {onNew ? (
            <Button variant="outline" onClick={onNew}>
              New upload link...
            </Button>
          ) : (
            <span />
          )}
          <Button onClick={onClose}>Done</Button>
        </div>
      }
    >
      {links.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : links.error ? (
        <Alert variant="destructive">
          <AlertDescription>The upload links could not be read. {(links.error as Error).message}</AlertDescription>
        </Alert>
      ) : rows.length === 0 ? (
        <div className="flex flex-col items-center gap-2 py-8 text-center">
          <span className="font-medium">No open upload links</span>
          <span className="text-sm text-muted-foreground">Make one to let a service send files into a folder.</span>
        </div>
      ) : (
        <>
          <p className="text-sm text-muted-foreground">
            Links you made, from here or through an agent, that can still take a file. A link isn't shown again; revoke one you
            no longer need.
          </p>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="h-[30px] px-2 font-medium">Folder</th>
                  <th className="px-2 font-medium">Type</th>
                  <th className="px-2 font-medium">Files</th>
                  <th className="px-2 font-medium">Expires</th>
                  <th className="px-2">
                    <span className="sr-only">Action</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {rows.map((l) => (
                  <tr key={l.id} data-testid="upload-link-row" className="border-b border-border/55">
                    <td className="h-10 px-2">{linkFolder(l)}</td>
                    <td className="px-2">{l.content_type}</td>
                    <td className="px-2 whitespace-nowrap">
                      {l.files_used} of {l.max_files} filed
                    </td>
                    <td className="px-2 whitespace-nowrap text-muted-foreground">{untilText(l.expires_at)}</td>
                    <td className="px-2 text-right whitespace-nowrap">
                      {confirming === l.id ? (
                        <>
                          <span className="mr-1.5 text-xs text-muted-foreground">Revoke this link?</span>
                          <Button size="xs" variant="outline" className="mr-1" onClick={() => setConfirming(null)}>
                            Keep
                          </Button>
                          <Button
                            size="xs"
                            variant="destructive"
                            disabled={revoke.isPending}
                            onClick={() => {
                              setFailed("");
                              setRevoked(false);
                              revoke.mutate(l.id, {
                                onSuccess: () => {
                                  setConfirming(null);
                                  setRevoked(true);
                                },
                                onError: () => setFailed("The link couldn't be revoked. Try again."),
                              });
                            }}
                          >
                            Revoke
                          </Button>
                        </>
                      ) : (
                        <Button
                          size="xs"
                          variant="outline"
                          className="border-destructive/30 text-destructive hover:bg-destructive/10"
                          onClick={() => setConfirming(l.id)}
                        >
                          Revoke
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {failed && (
            <Alert variant="destructive">
              <AlertDescription>{failed}</AlertDescription>
            </Alert>
          )}
          {revoked && (
            <p role="status" className="text-sm">
              Upload link revoked
            </p>
          )}
          <p className="text-xs text-muted-foreground">A revoked link refuses every upload from then on. Files it already filed stay.</p>
        </>
      )}
    </ModalShell>
  );
}
