import { RuleTester } from "eslint";
import local from "./index.js";

// local/no-bare-name-sort (#2389): a bare .sort() and any localeCompare are
// refused; a sort given an explicit comparator passes.
const tester = new RuleTester({ languageOptions: { ecmaVersion: 2022, sourceType: "module" } });

tester.run("no-bare-name-sort", local.rules["no-bare-name-sort"], {
  valid: [
    "names.sort(compareNames);",
    "rows.sort(byName((r) => r.name));",
    "days.sort((a, b) => a - b);",
    "keys.sort(compareOrdinal).join('');",
    "obj['sort']();",
    "sort();",
  ],
  invalid: [
    { code: "names.sort();", errors: [{ messageId: "bareSort" }] },
    { code: "[...new Set(xs)].sort().map(f);", errors: [{ messageId: "bareSort" }] },
    { code: "list.sort((a, b) => a.name.localeCompare(b.name));", errors: [{ messageId: "localeCompare" }] },
    { code: "a.localeCompare(b, undefined, { numeric: true });", errors: [{ messageId: "localeCompare" }] },
  ],
});
