# Digikala data dictionary

Only fields observed in the supplied endpoint research are included.

| Source path | Canonical destination | Normalization / constraint |
| --- | --- | --- |
| `product.id` | `Product.marketplaceProductId` | Integer; no transformation |
| `product.url.uri` | `Product.canonicalUrl` | Strip slug; retain `dkp-{id}` and prefix origin |
| `product.title_fa`, `title_en` | localized `Product.title` | NFC; optional English title may be empty |
| `product.status` | availability status | `marketable` / `out_of_stock` mapping |
| `product.brand`, `product.category` | `Brand`, `Category` refs | Retain native id/code/title |
| `product.breadcrumb[]` | category ancestors | Drop root and final self node |
| `product.specifications[]` | `ProductContent.specifications` | Preserve grouped/category-specific attributes |
| `product.images`, `videos` | `ProductMedia[]` | Base URL is identity; retain transform variants |
| `product.colors[]` | variant colour option | Use provided code/hex when present |
| `product.default_variant` | default offer | Empty object is valid when unavailable |
| `product.variants[]` | `Offer[]` / `ProductVariant[]` | Empty array valid when unavailable |
| `variants[].id` | offer / variant native id | A sampled id represents seller × colour × warranty |
| `variants[].seller` | `Seller` / offer seller ref | Seller performance `total_rate` is 0–100 |
| `variants[].warranty` | offer warranty | Nullable |
| `variants[].price.selling_price`, `rrp_price` | `PriceObservation` | Raw integers are Rial; unit must be `IRR-rial` |
| `price.discount_percent`, promotion/badge flags | `Promotion` | Preserve flag and label separately |
| `price.marketable_stock` | stock signal | Signal only; do not assert exact inventory |
| Detail `product.rating` | review aggregate rating | Detail surface is 0–5 |
| Listing `rating` | review aggregate rating | Observed as 0–100; division by 20 is partially verified inference |
| comments/questions counts | aggregate counts | Preserve separately |
| `comments_overview` | AI summary | Mark generated; never create a review |
| `last_comments[].user_name`, `last_questions[].sender` | none | Unconditionally redact/drop |
| comment/question dates | review/question dates | Persian digits→ASCII; Jalali→Gregorian ISO |
| comment reactions/buyer flag | review votes/verified flag | Preserve numeric values/boolean |
| listing pager/filters | `SearchObservation` | Facets are raw category-namespaced passthrough |
| sponsored brand / `properties.is_ad` | discovery sponsorship | Preserve explicit evidence; only `false` item flag observed |
| `/v1/user/init/`.is_logged_in | runtime state only | Never persist session/user data |
