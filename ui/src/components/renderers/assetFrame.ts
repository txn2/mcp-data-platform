/**
 * The sandbox an HTML or JSX asset's frame runs in, in the portal and the
 * public viewer alike.
 *
 * `allow-scripts` without `allow-same-origin` gives the artifact an opaque
 * origin, with no reach into the viewer's origin or storage: that is the
 * isolation. `allow-downloads` lets a download the reader starts go ahead (a
 * CSV a page builds from a Blob, or a declared file its download anchor
 * names), which every browser following the HTML spec otherwise blocks inside
 * a sandbox (#2127). It grants the frame nothing it could not already read:
 * the page can fetch a declared file and draw it.
 */
export const ASSET_FRAME_SANDBOX = "allow-scripts allow-downloads";

/**
 * The features an HTML asset's frame may hand the players it embeds (#2069):
 * fullscreen for Present and a player's own button, and autoplay, protected
 * media and picture-in-picture for a video player from the deployment's embed
 * hosts. A player outside that list is refused by the frame's policy before it
 * loads, so these reach only a listed host's frame.
 */
export const ASSET_FRAME_ALLOW = "fullscreen; autoplay; encrypted-media; picture-in-picture";
