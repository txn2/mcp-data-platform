import { useState } from "react";
import { ArrowLeft } from "lucide-react";
import {
  useCreateGlossaryTerm,
  useCreateGlossaryNode,
  type GlossaryNode,
} from "@/api/portal/datahub";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { CancelButton, MutationError } from "../catalog/primitives";
import { shortUrn } from "../catalog/utils";

// GlossaryEntityForm creates a term or a node under the branch being browsed.
// Both kinds take the same three fields, so they share the form and differ only
// in wording and in which write runs.
export function GlossaryEntityForm({
  conn,
  kind,
  parent,
  onDone,
}: {
  conn: string;
  kind: "term" | "node";
  parent: GlossaryNode | null;
  onDone: () => void;
}) {
  const createTerm = useCreateGlossaryTerm(conn);
  const createNode = useCreateGlossaryNode(conn);
  const create = kind === "term" ? createTerm : createNode;
  const [name, setName] = useState("");
  const [definition, setDefinition] = useState("");

  const copy =
    kind === "term"
      ? {
          heading: "New term",
          namePlaceholder: "e.g. Net Revenue",
          definitionPlaceholder: "What this term means, and how it is calculated.",
          submit: "Create term",
        }
      : {
          heading: "New node",
          namePlaceholder: "e.g. Finance",
          definitionPlaceholder: "What this part of the glossary covers.",
          submit: "Create node",
        };

  return (
    <div className="space-y-4">
      <button
        onClick={onDone}
        className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="size-4" /> Cancel
      </button>
      <div>
        <h2 className="text-lg font-semibold">{copy.heading}</h2>
        <p className="text-sm text-muted-foreground">
          {parent
            ? `Created in ${parent.name || shortUrn(parent.urn)}.`
            : "Created at the root of the glossary."}
        </p>
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="glossary-entity-name">Name</Label>
        <Input
          id="glossary-entity-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={copy.namePlaceholder}
        />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="glossary-entity-definition">Definition</Label>
        <Textarea
          id="glossary-entity-definition"
          value={definition}
          onChange={(e) => setDefinition(e.target.value)}
          rows={3}
          placeholder={copy.definitionPlaceholder}
        />
      </div>

      <p className="text-xs text-muted-foreground">
        DataHub indexes the glossary asynchronously, so what you create may take a moment to appear
        in the branch.
      </p>

      <MutationError mut={create} />

      <div className="flex gap-2">
        <Button
          onClick={() =>
            create.mutate(
              {
                name: name.trim(),
                definition: definition.trim() || undefined,
                parent_node: parent?.urn,
              },
              { onSuccess: onDone },
            )
          }
          disabled={name.trim() === "" || create.isPending}
        >
          {copy.submit}
        </Button>
        <CancelButton onClick={onDone} />
      </div>
    </div>
  );
}
