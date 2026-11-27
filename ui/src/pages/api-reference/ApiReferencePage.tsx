import { ExternalLink } from "lucide-react";
import { RedocStandalone } from "redoc";

import { Button } from "@/components/ui/button";
import { API_SPEC_URL, SWAGGER_UI_URL } from "@/lib/apiDocs";
import { redocOptions } from "./redocTheme";
import "./redoc.css";

// The platform's own REST surface, read in the portal (#1742).
//
// The platform already serves the document and a Swagger UI over it, but
// nothing in the product said so: an operator learned the URL from
// docs/server/admin-api.md or not at all. This is that document as a page of
// the portal, three-column, for the reader who is looking something up rather
// than sending a request. Swagger UI stays one click away for the reader who
// wants "Try it out", and is linked from here rather than from the rail,
// because it is not a section of the portal and a nav item that navigated out
// of the SPA would be the only one that did.
//
// ReDoc comes from the bundle. A <script> from a CDN was rejected for this: an
// air-gapped deployment has no CDN, and the page has to work there. It is a
// large dependency, so the page it is imported by is one of the shell's lazy
// routes (see AdminPages) and none of it is downloaded until this page opens.

const OPTIONS = redocOptions();

/**
 * REFERENCE_BASE_PATH is where ReDoc mounts its own routes. ReDoc 3 gives each
 * section, tag and operation an address of its own and writes them as links
 * from the root unless told otherwise, which would send a reader clicking
 * "Gateway" to /gateway, out of the portal.
 */
export const REFERENCE_BASE_PATH = "/portal/admin/api-reference";

export function ApiReferencePage() {

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="max-w-3xl text-xs text-muted-foreground">
          Every REST route this platform serves: the admin and portal APIs, the
          gateway routes a non-MCP caller invokes a connection through, and what
          each one authenticates with and answers.
        </p>
        <Button asChild variant="outline" size="sm" className="text-foreground">
          <a href={SWAGGER_UI_URL} target="_blank" rel="noopener noreferrer">
            <ExternalLink />
            Try it in Swagger UI
          </a>
        </Button>
      </div>

      {/* No overflow on the container, because ReDoc's menu is
          `position: sticky` and an ancestor with any other overflow becomes the
          scroll container it sticks to -- one that never scrolls, leaving the
          menu pinned to the top of the document instead of following. The
          options never change, so they are made once, outside the render. */}
      <div className="redoc-host rounded-lg border bg-card">
        <RedocStandalone
          specUrl={API_SPEC_URL}
          basePath={REFERENCE_BASE_PATH}
          options={OPTIONS}
        />
      </div>
    </div>
  );
}
