// Embed hosts (#2069): the https origins an HTML asset may frame and play
// media from, in the portal and on share links.
export interface EmbedHostsSettings {
  /** The list in force. */
  hosts: string[];
  /** The configuration file's list (or the platform default), which a saved list replaces. */
  configured: string[];
}
