import type {
  APIOperationDetail,
  GraphQLArgument,
  GraphQLFieldNode,
} from "@/api/apis/types";

// What an operation takes and gives back, for a spec entry holding a GraphQL
// schema (#1745). An OpenAPI operation says that in parameters, a request body
// and per-status responses; a GraphQL one says it in arguments, the input
// types they reference, and the shape it returns. The pane renders whichever
// the operation has, in the same order and under the same section styling, so
// one page reads either format.

/** SECTION_HEADING matches the OpenAPI sections' label styling, so the two
 * formats read as one pane rather than two. */
const SECTION_HEADING =
  "text-[11px] font-semibold uppercase tracking-wide text-muted-foreground";

/** ArgumentRow is one argument: what it is called, whether a document must
 * supply it, its type, and the default the schema declares. */
function ArgumentRow({ argument }: { argument: GraphQLArgument }) {
  return (
    <li className="px-3 py-2">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <code className="font-mono text-[12px]">{argument.name}</code>
        {argument.required ? (
          <span className="text-[10px] font-semibold uppercase tracking-wide text-destructive">
            required
          </span>
        ) : (
          <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
            optional
          </span>
        )}
        <code className="font-mono text-[11px] text-muted-foreground">{argument.type}</code>
      </div>
      {argument.description && (
        <p className="mt-0.5 text-[11px] text-muted-foreground">{argument.description}</p>
      )}
      {argument.default && (
        <p className="mt-0.5 font-mono text-[10px] text-muted-foreground/80">
          default {argument.default}
        </p>
      )}
    </li>
  );
}

/** ArgumentList renders a titled set of arguments, or nothing when there are
 * none to render. */
function ArgumentList({ title, arguments: args }: { title: string; arguments?: GraphQLArgument[] }) {
  if (!args || args.length === 0) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>{title}</h4>
      <ul className="divide-y rounded-md border">
        {args.map((a) => (
          <ArgumentRow key={a.name} argument={a} />
        ))}
      </ul>
    </div>
  );
}

/** InputTypesSection expands the input-object types the arguments reference,
 * so a caller filling in a filter or a create payload does not need a second
 * lookup. */
function InputTypesSection({ detail }: { detail: APIOperationDetail }) {
  const types = detail.graphql_input_types;
  if (!types || types.length === 0) return null;
  return (
    <div className="space-y-2">
      <h4 className={SECTION_HEADING}>Input types</h4>
      {types.map((t) => (
        <div key={t.name} className="space-y-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <code className="font-mono text-[12px]">{t.name}</code>
          </div>
          {t.description && (
            <p className="text-[11px] text-muted-foreground">{t.description}</p>
          )}
          <ul className="divide-y rounded-md border">
            {(t.fields ?? []).map((f) => (
              <ArgumentRow key={f.name} argument={f} />
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

/** FieldNodeRow is one node of the return shape, with its own sub-fields
 * indented beneath it. The tree is already depth-limited by the platform, so
 * this renders what it was given rather than capping again. */
function FieldNodeRow({ node }: { node: GraphQLFieldNode }) {
  return (
    <li>
      <div className="flex flex-wrap items-baseline gap-x-2 py-0.5">
        <code className="font-mono text-[12px]">{node.name}</code>
        <code className="font-mono text-[11px] text-muted-foreground">{node.type}</code>
        {node.deprecated && (
          <span className="text-[10px] uppercase tracking-wide text-muted-foreground">
            deprecated
          </span>
        )}
      </div>
      {node.description && (
        <p className="text-[11px] text-muted-foreground">{node.description}</p>
      )}
      {node.fields && node.fields.length > 0 && (
        <ul className="ml-3 border-l pl-3">
          {node.fields.map((child) => (
            <FieldNodeRow key={child.name} node={child} />
          ))}
        </ul>
      )}
    </li>
  );
}

/** ReturnShapeSection renders the tree the operation returns. */
function ReturnShapeSection({ detail }: { detail: APIOperationDetail }) {
  const shape = detail.graphql_return_shape;
  if (!shape || shape.length === 0) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>
        Returns
        {detail.return_type && (
          <code className="ml-2 font-mono text-[11px] normal-case tracking-normal">
            {detail.return_type}
          </code>
        )}
      </h4>
      <ul className="rounded-md border px-3 py-2">
        {shape.map((node) => (
          <FieldNodeRow key={node.name} node={node} />
        ))}
      </ul>
    </div>
  );
}

/** DocumentSection is the load-bearing part of the pane for this format: a
 * document that already validates against the schema and calls the operation,
 * with a variables object ready to edit. It is the difference between a
 * correct call on the first try and several spent on validation errors. */
function DocumentSection({ detail }: { detail: APIOperationDetail }) {
  if (!detail.graphql_skeleton) return null;
  return (
    <div className="space-y-1">
      <h4 className={SECTION_HEADING}>Document</h4>
      <pre className="overflow-x-auto rounded-md border px-3 py-2 font-mono text-[11px]">
        {detail.graphql_skeleton}
      </pre>
      {detail.graphql_variables && (
        <>
          <h4 className={SECTION_HEADING}>Variables</h4>
          <pre className="overflow-x-auto rounded-md border px-3 py-2 font-mono text-[11px]">
            {detail.graphql_variables}
          </pre>
        </>
      )}
    </div>
  );
}

/** GraphQLOperationSections renders everything a GraphQL operation says about
 * itself, and nothing at all for an operation that is not one. */
export function GraphQLOperationSections({ detail }: { detail: APIOperationDetail }) {
  return (
    <>
      <ArgumentList title="Arguments" arguments={detail.graphql_arguments} />
      <InputTypesSection detail={detail} />
      <ReturnShapeSection detail={detail} />
      <DocumentSection detail={detail} />
    </>
  );
}
