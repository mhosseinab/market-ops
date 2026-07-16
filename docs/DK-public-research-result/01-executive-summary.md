# Executive summary

## Research boundary

Chrome automation, guest/logged-out session, Persian locale, and desktop
viewport were used on 2026-07-16. No login, CAPTCHA, or bot-protection bypass
was attempted. Findings are limited to network requests a normal browser
session made itself.

## Verified findings

- Digikala is a Next.js pages-router CSR application. `__NEXT_DATA__` had
  empty `pageProps`; JSON-LD was absent in the pages tested.
- The web client fetches product and listing JSON from `api.digikala.com`.
- `GET /v2/product/{product_id}/` contains product identity, content, media,
  specifications, rating, embedded recent comments/questions, and all sampled
  variants/offers. A nine-offer product returned all offers in one response.
  Selecting a colour triggered no additional request in the observed session.
- Category/search and seller listings use
  `/discovery/api/v1/categories/{code}` and
  `/discovery/api/v1/sellers/{code}`. Observed responses contain products,
  filters, sort options, pager data, and sponsored-brand data.
- API prices are Rial; the UI displays Toman. The supplied evidence matched
  `450000000` Rial to `۴۵,۰۰۰,۰۰۰ تومان` (Rial / 10).
- Sampled unavailable products returned `status: out_of_stock`, an empty
  `default_variant` object, and an empty `variants` array.
- Review date strings were Jalali. Display names may be personal data and must
  be dropped or redacted at ingestion.

## Collection strategy

Prefer the public JSON requests the rendered page already makes. Use semantic
DOM extraction only as a validation or fallback layer. Do not interact with
authentication, cart, analytics, or feature-flag endpoints.

## Open questions

- The observed paginated comments URL returned HTTP 404 despite comments
  rendering in the UI. Its actual pagination mechanism was not established.
- Non-premium brand storefronts were not investigated.
- `/v1/dictionaries/` returned HTTP 400 without undiscovered parameters.
- Only mobile-phone and laptop category structures were sampled.

## Principal risk

The endpoints are undocumented and unversioned. Connector implementation must
use contract tests and schema-drift detection before treating any response
shape as stable.
