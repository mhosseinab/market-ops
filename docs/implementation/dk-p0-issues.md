# DK Marketplace Intelligence P0 — Blocked-Step Issues (`dk-p0-issues.md`)

**Fallback issue log — used only when GitHub is unreachable** (`gh` unauthenticated or no remote). The primary tracker is GitHub: `gh issue create --title "dk-p0 S<N> blocked: <step title>" --label dk-p0,blocked-step`. Append-only; never edit or delete an entry — add a `Resolution` line instead. Every entry is mirrored to GitHub as soon as it becomes reachable, then marked `migrated: <issue URL>`.

Each entry (same body as the GitHub issue template):

```
## ISSUE-<seq>: S<N> blocked — <step title>            (filed <date>)
- Step: S<N> (<phase>) · Branch: dk-p0/S<N> · Last SHA: <sha> · Attempts: 3
- Goal (from steps doc): <one line>
- Outstanding reviewer findings (verbatim, file:line):
  1. <finding>
  2. <finding>
- Final Verify output (summary + failing command): <paste>
- Suspected root cause: <one or two lines>
- Change requests / decision needed to unblock: <concrete list>
- Dependents held: <step IDs>
- Resolution: <(empty until resolved) — re-run outcome + SHA, or human descope ref in plan §11>
```

---

*(no entries yet)*
