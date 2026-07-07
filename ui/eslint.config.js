import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

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
      // Size ratchet against component regrowth (#766), mirroring the Go
      // package_budget_test.go budget. A warning, not an error: it flags
      // oversized modules for a follow-up split without blocking the build,
      // since several large pages/fixtures pre-date the budget. Split a file
      // by responsibility when it trips this — see pages/settings/connections/
      // and pages/settings/persona/ for the pattern.
      "max-lines": ["warn", 600],
    },
  },
  {
    // Exempt machine-generated types and test/mock fixtures: these are not
    // hand-maintained component source, so a line budget is noise there.
    files: [
      "src/api/generated/**/*.{ts,tsx}",
      "src/mocks/**/*.{ts,tsx}",
      "**/*.test.{ts,tsx}",
    ],
    rules: {
      "max-lines": "off",
    },
  },
);
