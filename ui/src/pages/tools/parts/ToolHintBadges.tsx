import type { ToolAnnotations } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";

// ToolHintBadges states what a client is told about the tool's behavior: the
// annotations tools/list advertises for it on this deployment, after any
// toolkit `annotations:` override (#1706). A client that honors them runs a
// read-only tool unasked and asks before a write, so these badges are how an
// operator checks which of the two a strict client will do.
//
// The MCP defaults decide the unstated cases: a tool with no annotations, or a
// write that leaves destructiveHint out, is treated as one that may destroy.
export function ToolHintBadges({ annotations }: { annotations?: ToolAnnotations }) {
  if (!annotations) {
    return (
      <Badge
        variant="muted"
        title="This tool advertises no annotations, so a client treats it as a write that may destroy data."
      >
        no hints
      </Badge>
    );
  }
  return (
    <>
      {annotations.readOnlyHint ? (
        <Badge variant="info" title="readOnlyHint: the tool does not modify its environment.">
          read-only
        </Badge>
      ) : annotations.destructiveHint === false ? (
        <Badge variant="warning" title="destructiveHint false: the tool only adds; it does not remove or overwrite.">
          additive
        </Badge>
      ) : (
        <Badge
          variant="danger"
          title={
            annotations.destructiveHint
              ? "destructiveHint: the tool may remove or overwrite existing state."
              : "destructiveHint is not stated, so a client treats this write as one that may destroy."
          }
        >
          destructive
        </Badge>
      )}
      {annotations.idempotentHint && (
        <Badge variant="muted" title="idempotentHint: calling it again with the same arguments changes nothing more.">
          idempotent
        </Badge>
      )}
    </>
  );
}
