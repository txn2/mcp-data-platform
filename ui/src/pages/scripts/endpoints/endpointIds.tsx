import { createContext, useContext } from "react";

// The ids an endpoint editor's labels point at, scoped to the editor (#2441):
// a script may hold several endpoint handlers, and with one being edited
// while another is added, two editors on one page must not share ids, or a
// label names the other editor's control. With no scope the ids are as
// written.

const EndpointIds = createContext("");

/** EndpointIdScope prefixes the ids of the endpoint editor inside it. */
export function EndpointIdScope({ prefix, children }: { prefix: string; children: React.ReactNode }) {
  return <EndpointIds.Provider value={prefix}>{children}</EndpointIds.Provider>;
}

/** useEndpointIds is the editor's id for a name. */
export function useEndpointIds(): (name: string) => string {
  const prefix = useContext(EndpointIds);
  return (name: string) => (prefix ? `${prefix}-${name}` : name);
}
