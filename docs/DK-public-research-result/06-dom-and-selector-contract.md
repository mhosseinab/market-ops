# DOM and selector contract

DOM is a fallback/validation layer. Prefer the observed API responses because
embedded JSON and JSON-LD were absent in the sampled pages. Never use generated
or utility/atomic CSS class names as primary selectors.

| Surface | Primary selector / rule | API fallback | Handling | Confidence |
| --- | --- | --- | --- | --- |
| Product URL | `link[rel="canonical"]` | Product URL | Strip query; force trailing slash | High |
| Product title | `document.title` | `product.title_fa` | API is canonical; DOM title has marketing prefix | High |
| Unavailable state | exact `ناموجود` badge | `status: out_of_stock` | Map to `OUT_OF_STOCK`; missing badge is valid for in-stock | High |
| Product code | `DKP-{digits}` near gallery | Canonical URL/product id | Redundant; API preferred | Medium |
| Breadcrumb | ordered links below navigation | `breadcrumb[]` | Drop Digikala root and final product node | High |
| Colour swatches | accessible Persian colour name | `colors[]`, `variants[]` | Do not click every swatch; API provides offer associations | High |
| Tabs | Persian tab labels | n/a | Key by label, not RTL position | High |
| Review card | star/date/body/votes/buyer-pill container | `last_comments[]` | ASCII digits; Jalali→ISO; redact name | Medium |
| AI review summary | card labelled `خلاصه دیدگاه‌های خریدارها` / `تولید شده با هوش مصنوعی` | `comments_overview` | Store with `generated: true`, never as review | High |
| Rating panel | numeral plus `از ۵` and count | `rating` | Record source scale; listing and detail scales differ | Medium |
| Listing card | product URL matching `/product/dkp-\d+/` | `products[]` | Virtualized grid: dedupe by product id | Medium |
| Filter panel | heading `فیلترها` and facet sections | `filters` | Facets are category-dependent | High/API |
| Sort control | labels matching `sort_options[].title_fa` | `sort_options[]` | Sort request parameter is unverified | High labels |
| Seller header | name/rating/grade | `seller` object | Trim only | High |

Review/question display names are personal data and must be removed before
storage from either API or DOM. The product detail API remains the source of
truth for variant prices, sellers, and warranties.
