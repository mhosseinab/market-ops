-- name: CreatePairingCode :one
-- Mint a short-lived, single-use pairing code (EXT-001). code_hash is the
-- SHA-256 of the raw code; the raw code is displayed to the user and never
-- reaches the database.
INSERT INTO extension_pairings (marketplace_account_id, code_hash, code_expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ClaimPairingCode :one
-- Atomically claim an unclaimed, unexpired, unrevoked pairing code, minting the
-- scoped capture credential in the same statement. The code_hash is cleared so a
-- code is strictly single-use; a second claim matches no row. Returns the row
-- (with credential id + expiry) only when the claim succeeds.
UPDATE extension_pairings
SET credential_hash       = $2,
    credential_expires_at = $3,
    claimed_at            = now(),
    code_hash             = NULL
WHERE code_hash = $1
  AND claimed_at IS NULL
  AND revoked_at IS NULL
  AND code_expires_at > now()
RETURNING *;

-- name: ResolveCaptureCredential :one
-- Resolve a presented capture credential to its scoped account. Rows that are
-- revoked or past credential expiry are excluded, so a revoked or stale
-- credential fails closed (401).
SELECT id, marketplace_account_id, credential_expires_at
FROM extension_pairings
WHERE credential_hash = $1
  AND revoked_at IS NULL
  AND credential_expires_at > now();

-- name: RevokePairingsForAccount :exec
-- Revoke every active capture credential for a marketplace account (EXT-001 kill
-- switch). Idempotent: already-revoked rows are left unchanged.
UPDATE extension_pairings
SET revoked_at = now()
WHERE marketplace_account_id = $1
  AND revoked_at IS NULL;

-- name: RevokeCaptureCredentialByID :execrows
-- Revoke EXACTLY ONE pairing record — the credential-scoped SELF-revoke the
-- extension calls so its kill switch invalidates authorization at the authority
-- that verifies it (issue #149, EXT-009). The id is the record the presented
-- capture credential resolved to; it is NEVER caller-supplied. Returns the
-- number of rows transitioned, so revoking an already-revoked credential is an
-- unambiguous no-op (0 rows) rather than an error — idempotent by construction.
-- It touches no other credential, so one device's revoke never kills another's.
UPDATE extension_pairings
SET revoked_at = now()
WHERE id = $1
  AND revoked_at IS NULL;

-- name: DeleteExpiredPairings :exec
-- Sweep pairings whose code and credential are both expired (housekeeping).
DELETE FROM extension_pairings
WHERE code_expires_at <= now()
  AND (credential_expires_at IS NULL OR credential_expires_at <= now());
