import { lazy, Suspense, useCallback } from "react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { Components } from "react-markdown";
import { EntityChip } from "@/components/knowledge/EntityChip";
import { isRefUrn, REF_TOKEN_SOURCE, trimRefToken, type ResolvedRef } from "@/lib/entityRefs";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type MdNode = { type?: string; value?: string; url?: string; children?: any[] };

function splitRefTokens(value: string): MdNode[] {
  const re = new RegExp(REF_TOKEN_SOURCE, "g");
  const out: MdNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(value)) !== null) {
    if (m.index > last) out.push({ type: "text", value: value.slice(last, m.index) });
    // Trailing sentence punctuation absorbed by the token's char class is split
    // back out so the chip url matches the (trimmed) resolved ref and the
    // punctuation renders as ordinary prose after the chip (#704).
    const token = trimRefToken(m[0]);
    out.push({ type: "link", url: token, children: [{ type: "text", value: token }] });
    const trailing = m[0].slice(token.length);
    if (trailing) out.push({ type: "text", value: trailing });
    last = m.index + m[0].length;
  }
  if (last === 0) return [{ type: "text", value }];
  if (last < value.length) out.push({ type: "text", value: value.slice(last) });
  return out;
}

function walkRefs(node: MdNode): void {
  if (!Array.isArray(node.children)) return;
  const next: MdNode[] = [];
  for (const child of node.children) {
    if (child.type === "text" && typeof child.value === "string") {
      next.push(...splitRefTokens(child.value));
    } else {
      // Do not descend into links (already a chip) or code (literal).
      if (child.type !== "link" && child.type !== "code" && child.type !== "inlineCode") {
        walkRefs(child);
      }
      next.push(child);
    }
  }
  node.children = next;
}

// remarkEntityRefs converts bare mcp:/urn: tokens in text into link nodes so they
// render as entity chips via the `a` override, even when the author wrote a bare
// token rather than a markdown link (#678).
function remarkEntityRefs() {
  return (tree: MdNode) => walkRefs(tree);
}

// Mermaid is loaded only when a document actually contains a diagram fence.
// See MermaidBlock for why it is a separate module.
const MermaidBlock = lazy(() => import("./MermaidBlock"));


export function MarkdownRenderer({
  content,
  bare,
  refs,
  onNavigate,
}: {
  content: string | null | undefined;
  bare?: boolean;
  refs?: Map<string, ResolvedRef>;
  onNavigate?: (path: string) => void;
}) {
  const components: Components = {
    // Render mcp:/urn: links as entity chips; ordinary links are unchanged.
    a: useCallback(
      ({ href, children, node: _node, ...rest }:
        React.ComponentProps<"a"> & { node?: unknown }) => {
        if (isRefUrn(href)) {
          const resolved = refs?.get(href as string);
          // A reference the viewer cannot access is shown as the author's link
          // text, never a confusing id chip.
          if (resolved && !resolved.accessible) {
            return <>{children}</>;
          }
          return <EntityChip urn={href as string} resolved={resolved} onNavigate={onNavigate} />;
        }
        return (
          <a href={href} {...rest}>
            {children}
          </a>
        );
      },
      [refs, onNavigate],
    ),
    code: useCallback(
      // react-markdown passes `node` (hast AST) — destructure it out so it
      // doesn't leak into the DOM as an invalid attribute.
      ({ className, children, node: _node, ...rest }:
        React.ComponentProps<"code"> & { node?: unknown }) => {
        const match = /language-(\w+)/.exec(className || "");
        const lang = match?.[1];
        const text = String(children).replace(/\n$/, "");

        if (lang === "mermaid") {
          return (
            <Suspense fallback={<div className="my-4 h-24 rounded-lg border border-border bg-muted/30" />}>
              <MermaidBlock content={text} />
            </Suspense>
          );
        }

        return (
          <code className={className} {...rest}>
            {children}
          </code>
        );
      },
      [],
    ),
    // Strip the `node` prop from <pre> as well to prevent DOM warnings.
    pre: useCallback(
      ({ node: _node, ...rest }: React.ComponentProps<"pre"> & { node?: unknown }) => (
        <pre {...rest} />
      ),
      [],
    ),
  };

  if (!content) return null;

  return (
    // prose-code:before/after:content-none suppresses Typography's inline-code
    // quotes: rendered markdown never displays the backticks of its own source.
    <div
      data-feedback-anchorable
      className={`prose prose-sm max-w-none dark:prose-invert prose-code:before:content-none prose-code:after:content-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0 ${bare ? "" : "rounded-lg border bg-card p-6"}`}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkEntityRefs]}
        components={components}
        // Preserve entity-reference URNs (mcp:/urn:) so the `a` override can chip
        // them; everything else keeps react-markdown's default URL sanitization.
        urlTransform={(url) => (isRefUrn(url) ? url : defaultUrlTransform(url))}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
}
