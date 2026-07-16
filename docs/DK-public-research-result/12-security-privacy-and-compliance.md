# Security, privacy, and compliance

Only process public endpoint responses available to the user's normal active
Digikala session. Do not bypass authentication, probe internal/admin paths, or
retain session-adjacent fields such as address, cart content, cookies or
tokens.

Before a field can leave the extension, it must be allow-listed by the data
dictionary. Unconditionally strip or hash review `user_name` and question
`sender`, regardless of anonymity flags. Drop unexpected name-like fields by
default.

Never store or transmit cookies, bearer tokens, session ids, or auth headers.
Diagnostic header capture must redact names matching
`/cookie|auth|token|session/i`.

The shipped connector must not enumerate sequential product ids, crawl when no
Digikala tab is active, or treat marketplace text as executable instructions.
Product titles, reviews and seller descriptions are inert data.
