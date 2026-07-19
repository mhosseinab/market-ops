#!/usr/bin/env node
// Copy-lint (LOC-002 / CHAT-084): UI components carry ZERO user-facing string
// literals — all display copy flows through catalog keys rendered via t(key).
//
// This is a PARSER/AST gate, not a regex scan (issue #16). Each .ts/.tsx file is
// parsed with the TypeScript compiler API and the syntax tree is walked, so a
// literal is classified by its SYNTACTIC POSITION rather than by how the
// surrounding text happens to look. It flags user-facing literals in:
//   - JSX text nodes                     (<span>Hello</span>)                [jsx-text]
//   - display JSX attributes             (aria-label="…", title=…, alt=…)    [jsx-attr]
//   - display JSX attribute expressions  (title={"…"} / {cond ? "a":"b"})    [jsx-attr-expr]
//   - JSX child expression containers    ({"…"}, {cond ? "a":"b"}, {`…`})    [jsx-expr]
//   - user-facing config object props    ({ label: "…", title: "…" } in .ts) [config-copy]
//   - vanilla-DOM display assignments    (el.textContent = "…")              [dom-prop]
//   - Persian/Arabic script in any file  (copy belongs in the locale pack)   [persian]
//
// It deliberately does NOT flag a literal that is an argument to a call
// (`t("nav.today")`), a catalog-key reference (`"route.today.title"`), a
// technical value (a path/URL/anchor/handle), a non-display attribute
// (className/role/id/data-*), or a non-display object property (path/key/id).
//
// Exemptions are explicit, narrow, and reviewable:
//   1. Test / story / harness files are exempt as a whole (they assert on copy).
//   2. An inline `// copylint-allow: <reason>` comment exempts the literal on the
//      same line or the immediately following line. A reason is MANDATORY — an
//      annotation with no reason is itself reported so the escape hatch stays
//      auditable.
//
// Usage:  node tools/copylint/copylint.mjs <dir> [<dir> …]
// Programmatic:  import { lintPaths, lintSource } from "./copylint.mjs"

import { readdirSync, readFileSync, statSync } from "node:fs";
import { extname, join } from "node:path";
import ts from "typescript";

const PERSIAN = /[؀-ۿ]/;
const LETTER = /[A-Za-z؀-ۿ]/;

// JSX attributes whose value is rendered to the user (visible text or announced
// to assistive tech). A literal here is display copy. Everything else
// (className, role, id, data-*, href, to, type, name, key, style, …) is a
// technical attribute and is never inspected.
const DISPLAY_JSX_ATTRS = new Set([
  "aria-label",
  "aria-description",
  "aria-placeholder",
  "aria-roledescription",
  "aria-valuetext",
  "aria-braillelabel",
  "aria-brailleroledescription",
  "title",
  "placeholder",
  "alt",
  "label",
]);

// Object-literal property names that carry display copy. Matched EXACTLY, so the
// catalog-key convention (`titleKey`, `labelKey`, `navLabelKey`) never collides
// with these — a `*Key` property holds a reference, not copy.
const DISPLAY_PROP_NAMES = new Set([
  "label",
  "title",
  "heading",
  "subtitle",
  "subhead",
  "text",
  "message",
  "description",
  "placeholder",
  "tooltip",
  "alt",
  "ariaLabel",
  "caption",
  "cta",
  "hint",
  "body",
  "header",
  "summary",
]);

// Vanilla-DOM UI surfaces (the extension popup/overlay: no JSX) set copy via a
// direct property assignment. A LITERAL assigned to a display property is the
// non-JSX equivalent of an inline JSX literal.
const DOM_DISPLAY_PROPS = new Set([
  "textContent",
  "innerText",
  "placeholder",
  "title",
  "alt",
  "ariaLabel",
]);

const EXEMPT_FILE = /(\.test\.[tj]sx?$)|(\.stories\.)|([\\/]test[\\/])/;
const ALLOW_DIRECTIVE = /copylint-allow\b/;
const ALLOW_WITH_REASON = /copylint-allow:\s*\S/;

// A dotted identifier chain — the catalog-key convention (`route.today.title`,
// `nav.today`, `needsReview.col.sku`). Segments may be camelCase; the shape has
// no spaces or sentence punctuation, so a value like this is a KEY reference,
// not display copy, wherever it appears.
const CATALOG_KEY_FORM = /^[A-Za-z][A-Za-z0-9]*(\.[A-Za-z0-9][A-Za-z0-9]*)+$/;

// Clearly-technical string values: a path, URL, anchor, protocol, or handle.
// Narrow on purpose — a bare word like "Save" is NOT technical and must flag.
const TECHNICAL_VALUE = /^(\/|\.\/|\.\.\/|https?:\/\/|mailto:|tel:|#|@[\w-]+$|[\w-]+:\/\/)/;

/** Does the raw string carry natural-language copy that belongs in the catalog? */
function isUserFacingValue(raw) {
  const text = raw.trim();
  if (text.length === 0) return false;
  if (!LETTER.test(text)) return false; // punctuation / digits only
  if (CATALOG_KEY_FORM.test(text)) return false; // a catalog-key reference
  if (TECHNICAL_VALUE.test(text)) return false; // path / URL / anchor / handle
  return true;
}

/** The property/attribute name as plain text, or undefined if computed/spread. */
function nameText(node) {
  if (!node) return undefined;
  if (ts.isIdentifier(node) || ts.isStringLiteral(node) || ts.isNumericLiteral(node)) {
    return node.text;
  }
  return undefined;
}

// The literal text of an expression IF it is a bare user-facing literal in a
// display position — a string, a no-substitution template, or a template with
// letter-bearing text. Returns the display text, or undefined when the
// expression is anything else (an identifier, a `t(…)` call, a JSX element, …).
// Deliberately does NOT descend into call arguments: `t("key")` yields nothing.
function literalDisplayText(expr) {
  if (!expr) return undefined;
  if (ts.isParenthesizedExpression(expr)) return literalDisplayText(expr.expression);
  if (ts.isStringLiteral(expr)) return expr.text;
  if (ts.isNoSubstitutionTemplateLiteral(expr)) return expr.text;
  if (ts.isTemplateExpression(expr)) {
    // A template in a display slot renders visible text; its literal spans are
    // copy even though interpolations are dynamic.
    const spans = expr.head.text + expr.templateSpans.map((s) => s.literal.text).join("");
    return LETTER.test(spans) ? spans : undefined;
  }
  return undefined;
}

// Collect every user-facing literal produced by an expression used in a display
// slot, following ternaries into both result branches. Each entry is
// { text, node }.
function collectDisplayLiterals(expr, out) {
  if (!expr) return;
  if (ts.isParenthesizedExpression(expr)) {
    collectDisplayLiterals(expr.expression, out);
    return;
  }
  if (ts.isConditionalExpression(expr)) {
    // Only the RESULT branches render; the condition is not display copy.
    collectDisplayLiterals(expr.whenTrue, out);
    collectDisplayLiterals(expr.whenFalse, out);
    return;
  }
  const text = literalDisplayText(expr);
  if (text !== undefined && isUserFacingValue(text)) out.push({ text, node: expr });
}

function scriptKind(file) {
  return extname(file) === ".tsx" ? ts.ScriptKind.TSX : ts.ScriptKind.TS;
}

/**
 * Lint a single source string. Returns an array of violations
 * { line, col, category, literal } with 1-based line/col, sorted by position.
 */
export function lintSource(fileName, text) {
  const sf = ts.createSourceFile(
    fileName,
    text,
    ts.ScriptTarget.Latest,
    /* setParentNodes */ true,
    scriptKind(fileName),
  );

  // Lines carrying a reviewed `copylint-allow: <reason>`. The exemption covers
  // the directive line and the next line (annotate the offending line above).
  const allowedLines = new Set();
  const found = [];
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (!ALLOW_DIRECTIVE.test(lines[i])) continue;
    if (!ALLOW_WITH_REASON.test(lines[i])) {
      // The escape hatch must justify itself; a bare directive is a violation.
      found.push({
        line: i + 1,
        col: lines[i].indexOf("copylint-allow") + 1,
        category: "exemption-without-reason",
        literal: lines[i].trim(),
      });
      continue;
    }
    allowedLines.add(i + 1);
    allowedLines.add(i + 2);
  }

  const posOf = (node) => {
    const { line, character } = sf.getLineAndCharacterOfPosition(node.getStart(sf));
    return { line: line + 1, col: character + 1 };
  };

  const push = (node, category, literal) => {
    const { line, col } = posOf(node);
    if (allowedLines.has(line)) return; // reviewed exemption
    found.push({ line, col, category, literal });
  };

  const visit = (node) => {
    // JSX text: a hardcoded run of visible copy.
    if (ts.isJsxText(node)) {
      const raw = node.text.replace(/\s+/g, " ").trim();
      if (raw.length > 0 && LETTER.test(raw)) push(node, "jsx-text", raw);
    }

    // JSX attributes: only the display allowlist is inspected.
    else if (ts.isJsxAttribute(node)) {
      const attr = nameText(node.name);
      if (attr && DISPLAY_JSX_ATTRS.has(attr) && node.initializer) {
        const init = node.initializer;
        if (ts.isStringLiteral(init)) {
          if (isUserFacingValue(init.text)) push(init, "jsx-attr", init.text);
        } else if (ts.isJsxExpression(init) && init.expression) {
          const hits = [];
          collectDisplayLiterals(init.expression, hits);
          for (const h of hits) push(h.node, "jsx-attr-expr", h.text);
        }
      }
    }

    // JSX child expression container: {"…"}, {cond ? "a" : "b"}, {`…`}.
    else if (ts.isJsxExpression(node) && node.expression) {
      const parent = node.parent;
      const isChild = parent && (ts.isJsxElement(parent) || ts.isJsxFragment(parent));
      if (isChild) {
        const hits = [];
        collectDisplayLiterals(node.expression, hits);
        for (const h of hits) push(h.node, "jsx-expr", h.text);
      }
    }

    // User-facing config object property: { label: "…", title: "…", … }.
    else if (ts.isPropertyAssignment(node)) {
      const key = nameText(node.name);
      if (key && DISPLAY_PROP_NAMES.has(key)) {
        const literal = literalDisplayText(node.initializer);
        if (literal !== undefined && isUserFacingValue(literal)) {
          push(node.initializer, "config-copy", literal);
        }
      }
    }

    // Vanilla-DOM display assignment: el.textContent = "…".
    else if (
      ts.isBinaryExpression(node) &&
      node.operatorToken.kind === ts.SyntaxKind.EqualsToken &&
      ts.isPropertyAccessExpression(node.left)
    ) {
      const prop = nameText(node.left.name);
      if (prop && DOM_DISPLAY_PROPS.has(prop)) {
        const literal = literalDisplayText(node.right);
        if (literal !== undefined && isUserFacingValue(literal)) {
          push(node.right, "dom-prop", literal);
        }
      }
    }

    ts.forEachChild(node, visit);
  };
  visit(sf);

  // File-level Persian guard: any Persian/Arabic script in a scanned component
  // is copy that belongs in the locale pack. Reported once, at first occurrence,
  // even if it sits somewhere the structural rules above do not reach.
  const p = PERSIAN.exec(text);
  if (p) {
    const before = text.slice(0, p.index);
    const line = before.split("\n").length;
    if (!allowedLines.has(line)) {
      found.push({
        line,
        col: p.index - before.lastIndexOf("\n"),
        category: "persian",
        literal: p[0],
      });
    }
  }

  found.sort((a, b) => a.line - b.line || a.col - b.col);
  return found;
}

function collect(dir, out) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (entry === "node_modules" || entry === "dist") continue;
      collect(full, out);
    } else if (/\.(ts|tsx)$/.test(full) && !EXEMPT_FILE.test(full)) {
      out.push(full);
    }
  }
}

const GUIDANCE = {
  "jsx-text": "inline JSX text — render via {t(key)}",
  "jsx-attr": "inline display-attribute literal — use t(key)",
  "jsx-attr-expr": "inline display-attribute literal — use t(key)",
  "jsx-expr": "inline JSX expression literal — use {t(key)}",
  "config-copy": "user-facing copy in config — store a catalog key (…Key)",
  "dom-prop": "inline DOM display literal — assign t(key)",
  persian: "Persian literal in component — move to the locale pack",
  "exemption-without-reason": "copylint-allow needs a reason (copylint-allow: why)",
};

/** Lint every .ts/.tsx file under the given paths. */
export function lintPaths(paths) {
  const files = [];
  for (const p of paths) collect(p, files);
  const violations = [];
  for (const file of files) {
    for (const v of lintSource(file, readFileSync(file, "utf8"))) {
      violations.push(
        `${file}:${v.line}:${v.col}  [${v.category}]  ${JSON.stringify(v.literal)} — ${
          GUIDANCE[v.category] ?? "user-facing literal"
        }`,
      );
    }
  }
  return { files, violations };
}

// CLI entry (skipped when imported by the self-test).
const invokedDirectly = process.argv[1] && import.meta.url === `file://${process.argv[1]}`;
if (invokedDirectly) {
  const targets = process.argv.slice(2);
  if (targets.length === 0) {
    console.error("copylint: no target directories given");
    process.exit(2);
  }
  const { files, violations } = lintPaths(targets);
  if (violations.length > 0) {
    console.error("copy-lint FAILED — inline copy found (LOC-002):\n");
    for (const v of violations) console.error(`  ${v}`);
    console.error(
      `\n${violations.length} violation(s). Move copy into packages/locale and render via t(key).`,
    );
    process.exit(1);
  }
  console.log(`copy-lint OK — ${files.length} component file(s), no inline copy.`);
}
