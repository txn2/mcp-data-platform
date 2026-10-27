import { CopyButton } from "@/components/provenance/parts";
import { absolutePageURL, embedSnippet } from "./embedSnippet";

/** FormEmbed offers a bound form's hosted page and its embed snippet. */
export function FormEmbed({ pageUrl, name, allowedOrigins }: { pageUrl: string; name: string; allowedOrigins: string[] }) {
  const url = absolutePageURL(pageUrl, window.location.origin);
  const snippet = embedSnippet(url, name);
  return (
    <div className="space-y-2 text-sm">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="font-medium">Embed on a site</h3>
        <CopyButton text={snippet} label="Copy the embed snippet" />
      </div>
      <p className="text-muted-foreground">
        The form&apos;s page is{" "}
        <a href={url} target="_blank" rel="noopener noreferrer" className="break-all underline">
          {url}
        </a>
        . A site shows it with this snippet, and only when Allowed origins lists the site&apos;s origin
        {allowedOrigins.length === 0 ? "; it lists none." : "."}
      </p>
      <pre className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs" aria-label="Embed snippet">
        {snippet}
      </pre>
    </div>
  );
}
