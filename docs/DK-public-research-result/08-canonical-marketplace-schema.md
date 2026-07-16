# Canonical marketplace schema

## Design rule

Core entities contain no Digikala-specific fields. Marketplace-specific fields
belong in `extensions.digikala`, leaving sibling extension namespaces available
for future marketplaces.

## Relationships

- Marketplace owns category tree, brands, and sellers.
- Product belongs to brand and category; has variants, media, content, and one
  review aggregate.
- ProductVariant has offers; Offer references one seller and produces price and
  availability time series.
- Search observations reference products by ranked position.
- Crawl runs own extraction evidence references; raw payloads remain in a
  sanitized fixture store, never duplicated on each entity.

## Observation envelope

Every observation requires: `marketplace`, `marketplaceEntityId`, `sourceUrl`,
`sourceType`, `capturedAt`, `locale`, `connectorVersion`, `schemaVersion`, and
`confidence`. It may add explicit `currencyOrUnit`, `evidenceRef`,
`rawFixtureRef`, and `parsingWarnings`.

`sourceType` values are: `public-web-endpoint`, `embedded-json`, `dom`, and
`user-triggered-request`. Confidence values are: `verified`,
`partially_verified`, and `unverified`.

## Core entity requirements

| Entity | Essential fields |
| --- | --- |
| Product | native product id, canonical URL, localized titles, brand/category refs, type, empty identifiers unless independently observed |
| ProductVariant | native variant id, product ref, nullable colour/warranty |
| Offer | native variant-derived id, seller ref, default flag, min/max quantity, fulfillment metadata |
| PriceObservation | offer ref, raw Rial amount/list amount, `IRR-rial`, discount/promotion, timestamp |
| AvailabilityObservation | offer ref or product-level unavailable special case; in-stock/out-of-stock/limited and optional stock signal |
| Seller | native seller id, name, URL, 0–100 rating with explicit source scale, badges/extensions |
| ReviewAggregate | product ref, 0–5 rating, counts, optional generated AI summary |
| Review | review id, product ref, rating/body/date/votes/buyer flag; reviewer identity always null/pseudonymous |
| SearchObservation | query/category, pager, sort/filter state, ranked product ids and sponsored flags |
| ExtractionEvidence | crawl run, source, HTTP status, timestamp, sanitized fixture reference |
| CrawlRun | versions, lifecycle, trigger, visited page types, counts, errors |

## Identity and deduplication

Products, variants, sellers, and categories upsert by `(marketplace, native id
or code)`. Offers are one-to-one with observed variant ids. Price and
availability observations are append-only; dedupe retries using same offer,
value/status and idempotency window. Search observations are append-only
because ranking changes are data. A vanished variant closes an offer history;
it must not delete historical evidence.
