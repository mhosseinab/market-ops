# Site map and page types

| Page type | URL pattern | Rendering | Status |
| --- | --- | --- | --- |
| Homepage | `https://www.digikala.com/` | CSR, Next.js shell | Verified |
| Search/category listing | `/search/category-{code}/` | CSR | Verified |
| Product detail | `/product/dkp-{numericId}/{slug}` | CSR | Verified |
| Seller storefront | `/seller/{SELLER_CODE}/` | CSR | Verified |
| Premium brand | `/brand/{code}/` → `/brand-landing/{code}/` | CSR | Redirect verified; one endpoint sample |
| Standard brand | `/brand/{code}/` | Unknown | Unverified |
| Reviews | Product-page tab | CSR | Summary data verified; pagination unresolved |
| Q&A | Product-page tab | CSR | Summary data verified; pagination unresolved |
| Landing/campaign | `/landing/{id}` | CSR | Deferred |
| Product collection | `/product-list/{plpCode}` | CSR | Deferred |
| Brand × category | `/{brandCode}/{categoryCode}` | Unknown | Deferred |

## Observed URL behaviour

Product canonical URLs drop the slug and use
`https://www.digikala.com/product/dkp-{id}/`. The search-category page accepts
observed query parameters including `q`, `page`, `has_selling_stock`,
`brands[]`, `price[min,max]`, and category-specific `attribute_{id}[]`.

The application uses SPA navigation after initial load. An extension must
observe history/navigation changes; document-load events alone are insufficient.

## MVP scope

Support homepage discovery, search/category listings, product details, seller
storefront listings, and review/Q&A summaries embedded in product payloads.

Defer landing pages, curated product lists, brand×category combinations,
standard brand storefronts, and dedicated paginated review/Q&A retrieval until
their schemas are independently verified.
