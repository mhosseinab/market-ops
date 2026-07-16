# Scraping workflows

## A. Product capture

1. Match `/product/dkp-{id}` and fetch `/v2/product/{id}/`.
2. Map product, variants, offers, media, content, and review aggregate.
3. If unavailable, accept empty `variants[]`/`default_variant`; model product
   unavailable state without inventing a price.
4. Validate URL/title/availability against the DOM; mismatch is drift signal,
   not a reason to block evidence.
5. Remove reviewer and question-author identifiers before mapping.
6. Emit evidence referencing a sanitized raw fixture.
7. Never click through colour swatches: sampled product responses already
   included all offers.

## B. Search/category capture

1. Match `/search/category-{code}/`.
2. Fetch listing with only `q` and `page` state already present in the URL.
3. Request later pages only in direct response to observed user scroll intent;
   never quietly crawl every page.
4. Record ranked items, sponsored brand slot, pager, filters, and sort state.

## C. Seller capture

1. Match `/seller/{code}/`.
2. Fetch lowercase seller endpoint code.
3. Upsert seller aggregates and a listing observation.
4. For seller-specific price/warranty detail, use product capture; the seller
   listing alone was not shown to contain full offer detail.

## Failure recovery

Log endpoint/status for non-200 responses. The supplied plan proposes at most
three retries with 2-second exponential backoff; this is a conservative rule,
not observed Digikala rate-limit behaviour. Do not retry the known comments
404 on every visit. Treat contradictory marketable status with no variants as
a validation/drift failure, not an automatic coercion.
