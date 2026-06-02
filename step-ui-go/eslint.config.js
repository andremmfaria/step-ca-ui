// ESLint v9 flat config — covers vanilla JS in static/js/
// sourceType: "script" because there is no bundler/module system
"use strict";

module.exports = [
  {
    files: ["static/js/**/*.js"],
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: "script",
      globals: {
        // Browser globals
        window: "readonly",
        document: "readonly",
        console: "readonly",
        alert: "readonly",
        confirm: "readonly",
        fetch: "readonly",
        location: "readonly",
        history: "readonly",
        setTimeout: "readonly",
        clearTimeout: "readonly",
        setInterval: "readonly",
        clearInterval: "readonly",
        FormData: "readonly",
        URLSearchParams: "readonly",
        XMLHttpRequest: "readonly",
        Event: "readonly",
        CustomEvent: "readonly",
        MutationObserver: "readonly",
        localStorage: "readonly",
        sessionStorage: "readonly",
        navigator: "readonly",
        HTMLElement: "readonly",
        Element: "readonly",
        NodeList: "readonly",
        DataTransfer: "readonly",
        File: "readonly",
        FileReader: "readonly",
        Blob: "readonly",
      },
    },
    rules: {
      // Security rules (P0-0 requirement)
      "no-eval": "error",
      "no-implied-eval": "error",
      "eqeqeq": ["error", "always"],
      // Basic quality
      "no-unused-vars": "warn",
      "no-undef": "error",
    },
  },
];
