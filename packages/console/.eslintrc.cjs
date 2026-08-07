// packages/console/.eslintrc.cjs
// eslint config for the console package. Flat-config is avoided so the pinned
// eslint 8 line stays stable under the S7 lockfile. Zero warnings allowed in CI.
module.exports = {
  root: true,
  env: { browser: true, es2020: true },
  extends: [
    "eslint:recommended",
    "plugin:@typescript-eslint/recommended",
    "plugin:react-hooks/recommended",
  ],
  parser: "@typescript-eslint/parser",
  parserOptions: { ecmaVersion: 2020, sourceType: "module" },
  plugins: ["@typescript-eslint", "react-refresh"],
  ignorePatterns: ["dist", "node_modules", ".eslintrc.cjs", "vite.config.ts"],
  rules: {
    "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
    "@typescript-eslint/no-explicit-any": "off",
    "@typescript-eslint/no-unused-vars": ["error", { argsIgnorePattern: "^_", varsIgnorePattern: "^_" }],
    "no-empty": ["error", { allowEmptyCatch: true }],
  },
};
