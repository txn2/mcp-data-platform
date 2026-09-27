// The tiles the scripts listing's grid shows (#1909), keyed "<script id>" and
// "<script id>-dark". They are not drawn by hand: each is the PNG the tile
// worker drew, in the headless renderer on the local stack, for the mocked
// script's source in ./scripts.ts. Imported by URL, so they load only with the
// mock server and never ship in the portal's own bundle.
import s001 from "./script-tiles/script-001.png?url";
import s001dark from "./script-tiles/script-001-dark.png?url";
import s002 from "./script-tiles/script-002.png?url";
import s002dark from "./script-tiles/script-002-dark.png?url";
import s003 from "./script-tiles/script-003.png?url";
import s003dark from "./script-tiles/script-003-dark.png?url";
import s004 from "./script-tiles/script-004.png?url";
import s004dark from "./script-tiles/script-004-dark.png?url";
import s005 from "./script-tiles/script-005.png?url";
import s005dark from "./script-tiles/script-005-dark.png?url";

export const mockScriptTiles: Record<string, string> = {
  "script-001": s001,
  "script-001-dark": s001dark,
  "script-002": s002,
  "script-002-dark": s002dark,
  "script-003": s003,
  "script-003-dark": s003dark,
  "script-004": s004,
  "script-004-dark": s004dark,
  "script-005": s005,
  "script-005-dark": s005dark,
};
