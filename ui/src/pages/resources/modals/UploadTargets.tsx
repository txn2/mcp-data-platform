import { Users } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const SCOPE_OPTIONS = [
  { value: "global", label: "Global" },
  { value: "persona", label: "Persona" },
  { value: "user", label: "User" },
];

// UploadTargets is the admin-only half of the upload form: which library the
// file lands in, and — for the fan-out scopes — which personas or which people.
// A reader has no choice to make here (their own scope is the only one they may
// write to), so the whole block is admin-only.
export function UploadTargets({
  scope,
  onScopeChange,
  personaNames,
  selectedPersonas,
  onTogglePersona,
  userEmails,
  onUserEmailsChange,
}: {
  scope: string;
  onScopeChange: (scope: string) => void;
  personaNames: string[];
  selectedPersonas: string[];
  onTogglePersona: (name: string) => void;
  userEmails: string;
  onUserEmailsChange: (emails: string) => void;
}) {
  const namedUsers = userEmails.split(",").filter((e) => e.trim()).length;
  return (
    <>
      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">Scope</Label>
        <Select value={scope} onValueChange={onScopeChange}>
          <SelectTrigger aria-label="Scope" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {SCOPE_OPTIONS.map((s) => (
              <SelectItem key={s.value} value={s.value}>
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {scope === "persona" && (
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">Personas</Label>
          <div className="max-h-32 space-y-0.5 overflow-y-auto rounded-md border bg-muted p-2">
            {personaNames.length === 0 ? (
              <p className="px-1 py-1 text-xs text-muted-foreground">No personas configured</p>
            ) : (
              personaNames.map((name) => (
                <Label
                  key={name}
                  className="cursor-pointer rounded px-2 py-1.5 font-normal hover:bg-muted"
                >
                  <input
                    type="checkbox"
                    checked={selectedPersonas.includes(name)}
                    onChange={() => onTogglePersona(name)}
                    className="rounded border-muted-foreground"
                  />
                  <Users className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                  {name}
                </Label>
              ))
            )}
          </div>
          {selectedPersonas.length > 0 && (
            <p className="text-xs text-muted-foreground">
              {selectedPersonas.length} selected — one resource will be created per persona
            </p>
          )}
        </div>
      )}

      {scope === "user" && (
        <div className="space-y-1">
          <Label htmlFor="upload-user-emails" className="text-xs text-muted-foreground">
            User emails (comma-separated)
          </Label>
          <Input
            id="upload-user-emails"
            value={userEmails}
            onChange={(e) => onUserEmailsChange(e.target.value)}
            placeholder="user@example.com, other@example.com"
          />
          {namedUsers > 1 && (
            <p className="text-xs text-muted-foreground">
              {namedUsers} users — one resource will be created per user
            </p>
          )}
        </div>
      )}
    </>
  );
}
