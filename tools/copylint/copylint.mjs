#!/usr/bin/env node
// Copy-lint (LOC-002 / CHAT-084): UI components carry ZERO string literals — all
// user-facing copy flows through catalog keys. This scans the given directories
// and fails on:
//   1. Persian/Arabic-script characters in a component (copy belongs in the
//      locale pack, never inline).
//   2. JSX TEXT nodes containing letters (a hardcoded literal instead of {t(…)}).
// Test/harness/story files are exempt (they legitimately assert on literals).
//
// Usage: node tools/copylint/copylint.mjs <dir> [<dir> …]

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

const PERSIAN = /[؀-ۿ]/;
// A JSX text run that STARTS with a letter right after a tag close `>` and holds
// no code punctuation — i.e. real display copy, not a generic type or expression.
const JSX_TEXT = />\s*([A-Za-z؀-ۿ][^<>{}=;:]*?)\s*</g;

// Vanilla-DOM UI surfaces (the extension popup/overlay: no JSX) carry copy via
// direct property assignment instead of a JSX text node. A LITERAL string
// assigned to one of these display properties is the non-JSX equivalent of an
// inline JSX literal — copy belongs in the locale pack, rendered via t(key).
// A call expression (e.g. `.textContent = t("x")`) does NOT match; template
// literals are exempt from this specific check (they legitimately interpolate
// already-translated values, e.g. `${label}: ${value}`) — a bare single/double
// quoted literal is the only pattern flagged here.
const DOM_PROP_LITERAL =
  /\.(textContent|placeholder|title|alt|ariaLabel)\s*=\s*(["'])([^"']*[A-Za-z][^"']*)\2/g;

const EXEMPT = /(\.test\.[tj]sx?$)|(\.stories\.)|([\\/]test[\\/])/;

function collect(dir, out) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (entry === "node_modules" || entry === "dist") continue;
      collect(full, out);
    } else if (/\.(ts|tsx)$/.test(full) && !EXEMPT.test(full)) {
      out.push(full);
    }
  }
}

function lineOf(text, index) {
  return text.slice(0, index).split("\n").length;
}

const targets = process.argv.slice(2);
if (targets.length === 0) {
  console.error("copylint: no target directories given");
  process.exit(2);
}

const files = [];
for (const t of targets) collect(t, files);

const violations = [];
for (const file of files) {
  const text = readFileSync(file, "utf8");

  const persian = text.match(PERSIAN);
  if (persian) {
    violations.push(
      `${file}:${lineOf(text, text.indexOf(persian[0]))}  Persian literal in component (move to locale pack)`,
    );
  }

  if (file.endsWith(".tsx")) {
    for (const m of text.matchAll(JSX_TEXT)) {
      const literal = m[1].trim();
      if (literal.length > 0) {
        violations.push(
          `${file}:${lineOf(text, m.index)}  inline JSX literal ${JSON.stringify(literal)} (use a catalog key)`,
        );
      }
    }
  }

  for (const m of text.matchAll(DOM_PROP_LITERAL)) {
    const literal = m[3].trim();
    if (literal.length > 0) {
      violations.push(
        `${file}:${lineOf(text, m.index)}  inline DOM property literal ${JSON.stringify(literal)} (use a catalog key via t())`,
      );
    }
  }
}

if (violations.length > 0) {
  console.error("copy-lint FAILED — inline copy found (LOC-002):\n");
  for (const v of violations) console.error(`  ${v}`);
  console.error(
    `\n${violations.length} violation(s). Move copy into packages/locale and render via t(key).`,
  );
  process.exit(1);
}

console.log(`copy-lint OK — ${files.length} component file(s), no inline copy.`);
