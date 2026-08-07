import { useCallback, useId, useState } from "react";
import { Plus } from "lucide-react";
import { useCreateUser } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// AddUserForm records someone who has not signed in yet, so they can be shared
// with before their first login. Extracted from UsersPanel.tsx (#1206).
export function AddUserForm({ onDone }: { onDone: () => void }) {
  const createMutation = useCreateUser();
  const ids = useId();
  const [email, setEmail] = useState("");
  const [first, setFirst] = useState("");
  const [last, setLast] = useState("");
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = useCallback(() => {
    if (!email.trim()) {
      setError("Email is required");
      return;
    }
    createMutation.mutate(
      {
        email: email.trim(),
        first_name: first.trim() || undefined,
        last_name: last.trim() || undefined,
      },
      {
        onSuccess: () => {
          setEmail("");
          setFirst("");
          setLast("");
          onDone();
        },
        onError: (err) =>
          setError(err instanceof Error ? err.message : "Failed to add user"),
      },
    );
  }, [email, first, last, createMutation, onDone]);

  return (
    <div className="space-y-3 border-b bg-muted/10 px-5 py-4">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-3 gap-3">
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-email`} className="gap-0.5 text-xs">
            Email
            <span className="text-destructive">*</span>
          </Label>
          <Input
            id={`${ids}-email`}
            type="email"
            value={email}
            onChange={(e) => {
              setEmail(e.target.value);
              setError(null);
            }}
            placeholder="e.g. marcus.johnson@example.com"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-first`} className="text-xs">
            First name
          </Label>
          <Input
            id={`${ids}-first`}
            type="text"
            value={first}
            onChange={(e) => setFirst(e.target.value)}
            placeholder="Marcus"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor={`${ids}-last`} className="text-xs">
            Last name
          </Label>
          <Input
            id={`${ids}-last`}
            type="text"
            value={last}
            onChange={(e) => setLast(e.target.value)}
            placeholder="Johnson"
          />
        </div>
      </div>
      <div className="flex justify-end">
        <Button
          type="button"
          size="sm"
          onClick={handleSubmit}
          disabled={createMutation.isPending || !email.trim()}
        >
          <Plus />
          {createMutation.isPending ? "Adding..." : "Add User"}
        </Button>
      </div>
    </div>
  );
}
