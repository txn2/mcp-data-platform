import type { RedocStandaloneProps } from "redoc";

/** RedocOptions is what RedocStandalone takes as `options`. */
export type RedocOptions = NonNullable<RedocStandaloneProps["options"]>;

/**
 * redocOptions is the whole of the page's configuration.
 *
 * `disableTelemetry`: ReDoc 3 sends OpenTelemetry traces of every page view
 * to Redocly's collector (otel.cloud.redocly.com) unless told not to. The
 * reference describes this deployment's admin surface and is read by its
 * administrators; nothing about either leaves the deployment, and an
 * air-gapped one has no route there anyway.
 *
 * The palette is not here. ReDoc 3 reads its colors from CSS custom
 * properties, so redoc.css maps them onto the portal's own tokens, which
 * already switch with the theme: the document follows a theme change without
 * being remounted.
 */
export function redocOptions(): RedocOptions {
  return { disableTelemetry: true };
}
