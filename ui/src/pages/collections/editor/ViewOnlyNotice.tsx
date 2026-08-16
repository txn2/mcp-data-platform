import { Eye } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";

interface Props {
  ownerEmail: string;
  onBack: () => void;
}

/**
 * What a reader with view access sees where the collection editor would be.
 * Reaching the editor by URL is not an error worth a bare refusal: the page
 * says what access they hold and who can widen it, rather than handing them a
 * form whose Save the server rejects.
 */
export function ViewOnlyNotice({ ownerEmail, onBack }: Props) {
  return (
    <EmptyState
      icon={Eye}
      action={
        <Button variant="outline" size="sm" onClick={onBack}>
          Back to collection
        </Button>
      }
    >
      <p className="font-medium">You have view access to this collection</p>
      <p className="text-xs">Editing needs an Editor share. Ask {ownerEmail} for one.</p>
    </EmptyState>
  );
}
