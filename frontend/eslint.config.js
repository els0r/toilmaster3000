// eslint keeps the stock recommended sets — @eslint/js, typescript-eslint,
// react-hooks (whose v7 recommended carries the React-Compiler-derived rules)
// and react-refresh. The point of this file is only to stop the defaults from
// hiding work: nothing is disabled, and `npm run lint` runs with
// --max-warnings 0 so a warning fails the same as an error.
import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
  },
);
