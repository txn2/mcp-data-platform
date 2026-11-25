/** The kinds of thing an application diagram draws (#2540). */
export type ApplicationNodeKind =
  | "script"
  | "library"
  | "asset"
  | "source"
  | "schema"
  | "table"
  | "channel"
  | "output"
  | "endpoint";

/** The kinds of link between two nodes of an application diagram. */
export type ApplicationLinkKind =
  | "event"
  | "reads"
  | "writes"
  | "loads"
  | "exports"
  | "refreshes"
  | "notifies"
  | "posts"
  | "calls"
  | "embeds"
  | "submits";

/** How a compared diagram marks a node or link. */
export type ApplicationDiagramChange = "added" | "removed" | "changed" | "unchanged";

/** One part, or what a part reaches, in an application diagram. */
export interface ApplicationDiagramNode {
  id: string;
  kind: ApplicationNodeKind;
  label: string;
  /** The group it is drawn in: application, schema or outside. */
  group: string;
  /** True for a part of the application; part_id is its id. */
  part: boolean;
  part_id?: string;
  /** The part's version the diagram is drawn at; absent for none. */
  version?: number;
  /** True for a name the source computes when it runs. */
  computed: boolean;
  /** What the node's card adds: a schedule, an endpoint, a connection. */
  detail: string[];
  change?: ApplicationDiagramChange;
  /** The version the compared diagram had, for a changed node. */
  was_version?: number;
}

/**
 * One link, from the part that acts to what it reaches: a script to the
 * table it reads or writes, a source to the script it starts.
 */
export interface ApplicationDiagramLink {
  from: string;
  to: string;
  kind: ApplicationLinkKind;
  /** The version a load names, the endpoint an asset calls. */
  detail?: string;
  /** A compared link whose detail moved is changed; was_detail is what it said. */
  change?: ApplicationDiagramChange;
  was_detail?: string;
}

/** A box nodes are drawn in; parent nests it in another. */
export interface ApplicationDiagramGroup {
  id: string;
  label: string;
  parent?: string;
}

/** Something the diagram finds wrong (#2540). */
export interface ApplicationFinding {
  /**
   * runtime_writes names, once, the automations that write a table named only
   * when they run, which may be a table reported unwritten (#2699).
   */
  kind: "library_version" | "unwritten_table" | "foreign_schema" | "runtime_writes";
  /** The node it is about. */
  node: string;
  message: string;
  /** True for a finding a release refuses. */
  blocks: boolean;
}

/** An application drawn at one version (#2540). */
export interface ApplicationDiagram {
  /** What was drawn: current, latest or a release's number. */
  version: string;
  compared_with?: string;
  nodes: ApplicationDiagramNode[];
  links: ApplicationDiagramLink[];
  groups: ApplicationDiagramGroup[];
  findings: ApplicationFinding[];
}
