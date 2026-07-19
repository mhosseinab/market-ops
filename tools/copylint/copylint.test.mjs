// Self-test for the copy-lint AST classifier (issue #16). Runs the linter over a
// committed fixture matrix and asserts EXACT findings per file: each positive
// fixture yields exactly its expected violation categories; each negative
// fixture (and every approved exclusion) yields none. Uses the Node built-in
// test runner so the gate needs no extra dependency:  node --test
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { lintSource } from "./copylint.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const fixture = (rel) => join(here, "fixtures", rel);
const categories = (rel) =>
  lintSource(fixture(rel), readFileSync(fixture(rel), "utf8")).map((v) => v.category);

// Every supported syntactic pattern → the exact categories it must produce.
const POSITIVE = {
  "positive/JsxText.tsx": ["jsx-text"],
  "positive/JsxAttr.tsx": ["jsx-attr", "jsx-attr", "jsx-attr", "jsx-attr"],
  "positive/JsxAttrExpr.tsx": ["jsx-attr-expr", "jsx-attr-expr", "jsx-attr-expr"],
  "positive/JsxExpr.tsx": ["jsx-expr"],
  "positive/JsxTernary.tsx": ["jsx-expr", "jsx-expr"],
  "positive/JsxTemplate.tsx": ["jsx-expr"],
  "positive/Fragment.tsx": ["jsx-text"],
  "positive/ConfigCopy.ts": ["config-copy", "config-copy", "config-copy"],
  "positive/DomProp.ts": ["dom-prop", "dom-prop"],
  "positive/Persian.tsx": ["persian"],
  // Exemption without a reason: reported, and it does NOT suppress the literal.
  "positive/ExemptNoReason.tsx": ["exemption-without-reason", "jsx-text"],
};

const NEGATIVE = [
  "negative/CatalogKeys.tsx",
  "negative/ConfigKeys.ts",
  "negative/TechnicalAttrs.tsx",
  "negative/NonDisplayObject.ts",
  "negative/EnumTernary.tsx",
  "negative/Exempted.tsx",
];

for (const [rel, expected] of Object.entries(POSITIVE)) {
  test(`positive fixture flags exactly ${expected.join(",")}: ${rel}`, () => {
    assert.deepEqual(categories(rel), expected);
  });
}

for (const rel of NEGATIVE) {
  test(`negative fixture is clean: ${rel}`, () => {
    assert.deepEqual(categories(rel), []);
  });
}

// Diagnostics carry file, 1-based location, category, and the detected literal.
test("a violation reports file, location, category and the literal", () => {
  const rel = "positive/JsxText.tsx";
  const [v] = lintSource(fixture(rel), readFileSync(fixture(rel), "utf8"));
  assert.equal(v.category, "jsx-text");
  assert.equal(v.literal, "Hello world");
  assert.ok(v.line >= 1 && v.col >= 1);
});
