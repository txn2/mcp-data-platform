import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import sonarjs from "eslint-plugin-sonarjs";
import importX from "eslint-plugin-import-x";
import { createTypeScriptImportResolver } from "eslint-import-resolver-typescript";
import tseslint from "typescript-eslint";

// Frontend complexity/coupling/layering gates (#816), the frontend analog of
// the Go `make verify` budgets: gocyclo <= 10 -> `complexity` <= 10, and
// gocognit <= 15 -> `sonarjs/cognitive-complexity` <= 15 (same algorithm and
// default threshold). Coupling is gated with `import-x/no-cycle` (a cycle is
// tight coupling by definition). Size proxies (`max-lines`,
// `max-lines-per-function`, `max-params`, `import-x/max-dependencies`) stay at
// `warn`: they flag regrowth for a follow-up split without blocking the build.
//
// Ratchet strategy (mirrors how the Go budgets were seeded): the error-level
// rules run against an ESLint bulk-suppressions baseline (`eslint-suppressions.json`),
// so existing violations do not fail CI while any NEW violation does. Regenerate
// the baseline only when intentionally splitting a legacy file:
//   npx eslint --suppress-rule complexity --suppress-rule sonarjs/cognitive-complexity
//   npx eslint --prune-suppressions   # drop entries a fixed file no longer needs
// See docs and the #816 triage list before adding to the baseline.
export default tseslint.config(
  // Build output and generated MSW service workers are not hand-edited source.
  { ignores: ["dist", "dist-content-viewer", "public/mockServiceWorker.js"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      "react-hooks": reactHooks,
      sonarjs,
      "import-x": importX,
    },
    settings: {
      // Resolve TS path aliases (`@/*` -> `src/*`, see tsconfig.json) and
      // extensionless imports so `import-x/no-cycle` can follow the real graph.
      "import-x/resolver-next": [
        createTypeScriptImportResolver({ project: "./tsconfig.json" }),
      ],
      // no-cycle traverses the graph by re-parsing each imported module to find
      // its own imports. Without mapping `.ts`/`.tsx` to the TS parser, that
      // re-parse fails silently on typed source and the return edge of a cycle
      // is never seen — the rule fails open. This mapping is what makes it real.
      "import-x/parsers": {
        "@typescript-eslint/parser": [".ts", ".tsx", ".cts", ".mts"],
      },
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      // Allow underscore-prefixed args/vars/destructure to mark intentionally unused.
      "@typescript-eslint/no-unused-vars": [
        "error",
        {
          argsIgnorePattern: "^_",
          varsIgnorePattern: "^_",
          caughtErrorsIgnorePattern: "^_",
          destructuredArrayIgnorePattern: "^_",
        },
      ],

      // --- Complexity budget (mirrors the Go per-function gates) ---
      // gocyclo <= 10 analog. Error-level: enforced on new/changed code via the
      // suppressions baseline for existing offenders.
      complexity: ["error", 10],
      // gocognit <= 15 analog (same algorithm, same default threshold).
      "sonarjs/cognitive-complexity": ["error", 15],

      // --- Coupling / layering ---
      // A cycle is tight coupling by definition; the highest-value structural
      // rule. Error-level. `maxDepth` bounds traversal cost on a large graph.
      "import-x/no-cycle": ["error", { maxDepth: 4, ignoreExternal: true }],

      // --- Size / fan-out proxies (advisory, follow-up split signal) ---
      // Per-file size backstop against component regrowth (#766). A warning, not
      // an error: several large pages/fixtures pre-date the budget. Split a file
      // by responsibility when it trips this — see pages/settings/connections/
      // and pages/settings/persona/ for the pattern.
      "max-lines": ["warn", 600],
      // Targets a giant component function specifically (what `max-lines` blends
      // into whole-file count). Blank lines/comments are not the smell.
      "max-lines-per-function": [
        "warn",
        { max: 250, skipBlankLines: true, skipComments: true },
      ],
      // Excessive positional args are a coupling smell for helpers/hooks. React
      // components take a single props object, so this rarely fires on them.
      "max-params": ["warn", 5],
      // Import fan-out as a cohesion proxy: a module pulling in too many others
      // is usually doing too much. Advisory; type-only imports are excluded.
      "import-x/max-dependencies": [
        "warn",
        { max: 25, ignoreTypeImports: true },
      ],

      // --- Copy-paste / dead-branch growth (sonarjs) ---
      "sonarjs/no-identical-functions": "warn",
      "sonarjs/no-collapsible-if": "warn",
    },
  },
  {
    // Exempt machine-generated types and test/mock fixtures: these are not
    // hand-maintained component source, so complexity/size budgets are noise
    // there. Kept in sync with the CI lint scope and the doc-stated thresholds.
    files: [
      "src/api/generated/**/*.{ts,tsx}",
      "src/mocks/**/*.{ts,tsx}",
      "**/*.test.{ts,tsx}",
    ],
    rules: {
      "max-lines": "off",
      "max-lines-per-function": "off",
      complexity: "off",
      "sonarjs/cognitive-complexity": "off",
      "sonarjs/no-identical-functions": "off",
      "max-params": "off",
      "import-x/max-dependencies": "off",
    },
  },
);
