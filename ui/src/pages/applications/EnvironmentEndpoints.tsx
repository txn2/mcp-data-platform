import type { ApplicationEndpoint } from "@/api/portal/hooks/applicationEndpoints";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { CopyButton } from "@/components/provenance/parts";

// EnvironmentEndpoints is an environment card's Address row (#2822): each
// endpoint the environment serves, with every URL it answers at there (the
// platform's host, the application's own address for the last environment,
// and each site environment of the environment's name), each one copyable.
// An application holding no endpoint shows None.
export function EnvironmentEndpoints({ env, endpoints }: { env: string; endpoints: ApplicationEndpoint[] }) {
  if (endpoints.length === 0) {
    return (
      <span className="text-muted-foreground" data-testid={`environment-endpoints-${env}`}>
        None
      </span>
    );
  }
  return (
    <ul className="flex min-w-0 flex-col gap-2" data-testid={`environment-endpoints-${env}`}>
      {endpoints.map((ep) => (
        <li key={ep.name} className="min-w-0">
          <div className="text-xs">
            <span className="font-medium">{ep.name}</span>
            <span className="text-muted-foreground">
              {" "}
              · {ep.methods.join(", ")} · {ep.script}
            </span>
          </div>
          <ul className="mt-0.5 flex flex-col gap-0.5">
            {ep.urls.map((url) => (
              <li key={url} className="flex min-w-0 items-start gap-1">
                <span className="min-w-0 break-all font-mono text-xs leading-5">{url}</span>
                <CopyButton text={url} label={`Copy ${url}`} />
              </li>
            ))}
          </ul>
        </li>
      ))}
    </ul>
  );
}

/** endpointsOf is what an environment serves, none when the page says nothing. */
export function endpointsOf(detail: ApplicationDetail, env: string): ApplicationEndpoint[] {
  return detail.endpoints?.[env] ?? [];
}
