import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import { useDirectoryNames } from "@/api/portal/hooks";
import { scanMentions, splitBody } from "@/lib/mentions";
import { cn } from "@/lib/utils";

// MdNode is the part of an mdast node the two plugins below read and write.
type MdNode = {
  type?: string;
  value?: string;
  children?: MdNode[];
  data?: { hName?: string; hProperties?: Record<string, string> };
};

/**
 * remarkMentions turns each @local(domain) token in prose into a node that
 * renders as a mention: a span carrying the address, which the span override
 * shows as a chip when the server recorded the mention as delivered and as the
 * token written otherwise. Code and links are left as written.
 */
function remarkMentions() {
  return (tree: MdNode) => walk(tree, (value) => {
    const out: MdNode[] = [];
    for (const s of splitBody(value)) {
      out.push(
        s.kind === "mention"
          ? { type: "mention", data: { hName: "span", hProperties: { "data-mention": s.email, "data-raw": s.text } } }
          : { type: "text", value: s.text },
      );
    }
    return out;
  }, (value) => scanMentions(value).length > 0);
}

/**
 * remarkSoftBreaks keeps the author's line breaks: a single newline inside a
 * paragraph is a break, as a plain-text comment always showed it (#2214).
 */
function remarkSoftBreaks() {
  return (tree: MdNode) => walk(tree, (value) => {
    const out: MdNode[] = [];
    value.split("\n").forEach((line, i) => {
      if (i > 0) out.push({ type: "break" });
      if (line) out.push({ type: "text", value: line });
    });
    return out;
  }, (value) => value.includes("\n"));
}

/** walk replaces every prose text node outside code and links that matches. */
function walk(node: MdNode, replace: (value: string) => MdNode[], matches: (value: string) => boolean): void {
  if (!Array.isArray(node.children)) return;
  const next: MdNode[] = [];
  for (const child of node.children) {
    if (child.type === "text" && typeof child.value === "string" && matches(child.value)) {
      next.push(...replace(child.value));
      continue;
    }
    if (child.type !== "link" && child.type !== "code" && child.type !== "inlineCode") {
      walk(child, replace, matches);
    }
    next.push(child);
  }
  node.children = next;
}

/**
 * CommentBody renders a feedback thread body as Markdown (#2214): emphasis,
 * code, lists, headings, tables and links, with the author's line breaks kept
 * and delivered @-mentions as chips. Raw HTML is dropped and a link with a
 * script URL is not followed, as the asset Markdown renderer treats them.
 *
 * A mention renders as a chip only when the server recorded it as delivered
 * (`metadata.mentions`); any other token reads as the text written.
 */
export function CommentBody({
  body,
  mentions,
  className,
}: {
  body: string;
  mentions?: string[];
  className?: string;
}) {
  const delivered = new Set((mentions ?? []).map((m) => m.toLowerCase()));
  const names = useDirectoryNames(scanMentions(body).flatMap((m) => (delivered.has(m.email) ? [m.email] : [])));

  const components: Components = {
    span: ({ node: _node, ...props }) => {
      const email = (props as Record<string, unknown>)["data-mention"];
      if (typeof email !== "string") return <span {...props} />;
      const raw = String((props as Record<string, unknown>)["data-raw"] ?? "");
      if (!delivered.has(email)) return <span>{raw}</span>;
      return (
        <span title={email} className="rounded bg-primary/10 px-1 font-medium text-primary">
          @{names[email] || email}
        </span>
      );
    },
    a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noopener noreferrer" />,
  };

  return (
    <div
      className={cn(
        "prose prose-sm dark:prose-invert max-w-none break-words prose-p:my-1 prose-ul:my-1 prose-ol:my-1 prose-pre:my-1 prose-headings:my-1.5",
        className,
      )}
      data-testid="comment-body"
    >
      <ReactMarkdown remarkPlugins={[remarkGfm, remarkMentions, remarkSoftBreaks]} components={components} skipHtml>
        {body}
      </ReactMarkdown>
    </div>
  );
}
