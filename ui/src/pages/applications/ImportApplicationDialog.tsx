import { useState } from "react";
import {
  bundleNames,
  useImportApplication,
  type ApplicationBundle,
} from "@/api/portal/hooks/applications";
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
import { errorText } from "./applicationFormat";
import { Field } from "./NewApplicationDialog";

// ImportApplicationDialog installs an exported release on this deployment
// (#2411). A bundle refers to connections, secrets, webhook sources and bot
// checks by name, and a name on the exporting deployment may be another name
// here, so every one is shown with its name on this deployment, prefilled with
// the same name. The server refuses a bundle naming something the map leaves
// out, and says which.
export function ImportApplicationDialog({
  onClose,
  onImported,
}: {
  onClose: () => void;
  onImported: (application: string) => void;
}) {
  const importApp = useImportApplication();
  const [bundle, setBundle] = useState<ApplicationBundle | null>(null);
  const [map, setMap] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<string | null>(null);

  const readFile = async (file: File | undefined) => {
    setFailure(null);
    setBundle(null);
    if (!file) return;
    const parsed = parseBundle(await file.text());
    if (!parsed) {
      setFailure("This file is not an application bundle.");
      return;
    }
    setBundle(parsed);
    setMap(Object.fromEntries(bundleNames(parsed).map((n) => [n, n])));
  };

  const submit = () => {
    if (!bundle) return;
    setFailure(null);
    importApp.mutate(
      { bundle, map },
      {
        onSuccess: (r) => onImported(r.application),
        onError: (e) => setFailure(errorText(e, "The bundle could not be imported")),
      },
    );
  };

  const names = Object.keys(map);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Import</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Field id="application-bundle" label="Bundle">
            <Input
              id="application-bundle"
              type="file"
              accept="application/json,.json"
              onChange={(e) => void readFile(e.target.files?.[0])}
            />
          </Field>
          {bundle && (
            <p className="text-sm">
              {bundle.application?.name ?? "—"}
              {bundle.release ? `, release ${bundle.release}` : ""}
            </p>
          )}
          {bundle && names.length > 0 && (
            <div className="space-y-2" data-testid="import-names">
              {names.map((n) => (
                <Field key={n} id={`import-name-${n}`} label={n}>
                  <Input
                    id={`import-name-${n}`}
                    value={map[n] ?? ""}
                    onChange={(e) => setMap({ ...map, [n]: e.target.value })}
                  />
                </Field>
              ))}
            </div>
          )}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={importApp.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!bundle || importApp.isPending}>
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** parseBundle reads an uploaded file as a bundle, or null when it is not one. */
export function parseBundle(text: string): ApplicationBundle | null {
  try {
    const v: unknown = JSON.parse(text);
    if (!v || typeof v !== "object" || Array.isArray(v)) return null;
    const b = v as ApplicationBundle;
    if (!b.application || typeof b.application !== "object") return null;
    return b;
  } catch {
    return null;
  }
}
