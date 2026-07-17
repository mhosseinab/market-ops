# gen/ts — generated TypeScript gateway client

**Committed, never hand-edited.** Produced by `task contracts:generate`
(openapi-typescript → `schema.d.ts` + a thin openapi-fetch wrapper) from
`contracts/gateway.openapi.yaml`. The generator wiring lands in **S4**; the
current contents are an S1 placeholder so this directory is a valid pnpm
workspace member (`workspace:*`) referenced by `apps/*` and `packages/locale`.

Excluded from linters (biome ignores `gen/ts`). Drift is guarded by
`task contracts:drift`.
