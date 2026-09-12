import { useState, useCallback } from "react";
import { Plus, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

// RecipientsEditor edits an operator alert's distribution list. The list is
// the whole audience for an alert, so it is edited explicitly rather than
// inferred from roles: the platform has no queryable set of admins, because
// admin is a claim on a token at request time rather than a stored property of
// a person.
//
// It is one component rather than one per alert (#1694): the review-queue
// alert and the connection-revocation escalation edit the same kind of list,
// and two copies could drift on what Enter, blur, or a duplicate address does.

export interface RecipientsEditorProps {
  recipients: string[];
  onChange: (next: string[]) => void;
  // label names the list. It differs per alert because the list means
  // different things: everyone who hears about a queue, versus the people an
  // unhandled revocation escalates to.
  label?: string;
  placeholder?: string;
  // help is the line under the field. It defaults to the one thing true of
  // every such list: a recipient's own preferences still apply.
  help?: string;
}

const defaultHelp =
  "Each recipient's own notification preferences still apply: someone who turned " +
  "email notifications off receives nothing.";

export function RecipientsEditor({
  recipients,
  onChange,
  label = "Recipients",
  placeholder = "data-admin@example.com",
  help = defaultHelp,
}: RecipientsEditorProps) {
  const [draft, setDraft] = useState("");

  const add = useCallback(() => {
    const value = draft.trim();
    if (!value || recipients.includes(value)) {
      setDraft("");
      return;
    }
    onChange([...recipients, value]);
    setDraft("");
  }, [draft, recipients, onChange]);

  return (
    <div className="space-y-1.5">
      {/* The heading names the list; the input's own aria-label names what
          typing in it does. Wiring the two together would replace "Add
          recipient" with "Recipients" on the control that adds one. */}
      <Label className="text-xs">{label}</Label>
      <div className="flex gap-2">
        <Input
          type="email"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          // Commit on blur as well, so an address typed but not added is not
          // silently dropped by the Save the operator reaches for next.
          onBlur={add}
          placeholder={placeholder}
          aria-label="Add recipient"
          className="font-mono"
        />
        <Button type="button" variant="outline" onClick={add} disabled={!draft.trim()}>
          <Plus />
          Add
        </Button>
      </div>
      {recipients.length > 0 && (
        <ul className="flex flex-wrap gap-1.5 pt-1">
          {recipients.map((email) => (
            <li key={email}>
              <Badge variant="outline" className="gap-1 bg-muted/40 py-1 pl-2.5 pr-1 font-mono">
                {email}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => onChange(recipients.filter((r) => r !== email))}
                  aria-label={`Remove ${email}`}
                  className="size-4 rounded-full"
                >
                  <X />
                </Button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-muted-foreground">{help}</p>
    </div>
  );
}
