import { useState } from "react";
import { useCreateApplication, type Application } from "@/api/portal/hooks/applications";
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
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { errorText } from "./applicationFormat";

// NewApplicationDialog records an application (#2411): its name, what it is,
// and, when it has one, the application database schema it owns. The caller
// becomes its owner.
export function NewApplicationDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (a: Application) => void;
}) {
  const create = useCreateApplication();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [connection, setConnection] = useState("");
  const [schema, setSchema] = useState("");
  const [failure, setFailure] = useState<string | null>(null);

  const submit = () => {
    setFailure(null);
    create.mutate(
      {
        name: name.trim(),
        description: description.trim(),
        ...(connection.trim() ? { connection: connection.trim() } : {}),
        ...(schema.trim() ? { schema: schema.trim() } : {}),
      },
      {
        onSuccess: onCreated,
        onError: (e) => setFailure(errorText(e, "The application could not be created")),
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New application</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <Field id="application-name" label="Name">
            <Input id="application-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field id="application-description" label="Description">
            <Textarea
              id="application-description"
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <div className="grid gap-3 sm:grid-cols-2">
            <Field id="application-connection" label="Connection">
              <Input
                id="application-connection"
                value={connection}
                onChange={(e) => setConnection(e.target.value)}
              />
            </Field>
            <Field id="application-schema" label="Schema">
              <Input id="application-schema" value={schema} onChange={(e) => setSchema(e.target.value)} />
            </Field>
          </div>
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={name.trim() === "" || create.isPending}>
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function Field({
  id,
  label,
  children,
}: {
  id: string;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {children}
    </div>
  );
}
