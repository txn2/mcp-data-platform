import { JsxRenderer } from "./JsxRenderer";
import { HtmlRenderer } from "./HtmlRenderer";
import { MarkdownRenderer } from "./MarkdownRenderer";
import { SvgRenderer } from "./SvgRenderer";
import { CsvRenderer } from "./CsvRenderer";

interface Props {
  contentType: string;
  content: string;
}

export function ContentRenderer({ contentType, content }: Props) {
  const ct = contentType.toLowerCase();

  if (ct.includes("jsx") || ct.includes("react")) {
    return <JsxRenderer content={content} />;
  }
  if (ct.includes("svg")) {
    return <SvgRenderer content={content} />;
  }
  if (ct.includes("markdown") || ct.endsWith(".md")) {
    return <MarkdownRenderer content={content} />;
  }
  if (ct.includes("html")) {
    return <HtmlRenderer content={content} />;
  }
  if (ct.includes("csv")) {
    return <CsvRenderer content={content} />;
  }

  // Fallback: plain text
  return (
    <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap">
      {content}
    </pre>
  );
}
