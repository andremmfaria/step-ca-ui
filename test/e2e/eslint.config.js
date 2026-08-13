// ESLint v9 flat config for the e2e harness, following the same shape as
// backend/eslint.config.js rather than introducing a second style.
"use strict";

const tseslint = require("typescript-eslint");

module.exports = tseslint.config(
  {
    ignores: ["node_modules/**", "artifacts/**", "playwright-report/**"],
  },
  ...tseslint.configs.recommended,
  {
    // This config file itself is CommonJS, matching backend/eslint.config.js.
    files: ["eslint.config.js"],
    rules: { "@typescript-eslint/no-require-imports": "off" },
  },
  {
    files: ["**/*.ts"],
    languageOptions: {
      parserOptions: { ecmaVersion: 2022, sourceType: "module" },
    },
    rules: {
      "no-eval": "error",
      "no-implied-eval": "error",
      eqeqeq: ["error", "always"],
      "@typescript-eslint/no-unused-vars": ["warn", { argsIgnorePattern: "^_" }],
      // Playwright fixtures are declared with an empty destructuring pattern.
      "@typescript-eslint/no-empty-object-type": "off",
    },
  },
);
