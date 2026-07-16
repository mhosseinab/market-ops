# Network API catalog

All paths below are relative to `https://api.digikala.com`. They are observed
web-client endpoints, not documented public APIs.

## Homepage manifest

`GET /discovery/api/v1/home`

- Purpose: homepage widget manifest.
- Observed envelope: `status`, then `data` with `container_id`, `title`,
  `description`, and `widgets`.
- Observed widget types include `dkms_banner`, `circle_badge`,
  `cart_based_products`, `horizontal_products`, `touchpoint_group`,
  `image_carousel`, `divider`, and `ads_video`.
- Use: discovery signal only; not canonical ranking.

## Lazy widget

`GET /discovery/api/v1/widget-factory/widget/{id}`

- Purpose: resolve an individual homepage widget.
- `horizontal_products` was observed with product summaries containing product
  id/title/status, image, price, rating, brand, and deep link.
- Other widget schemas were not fully inspected.

## Category/search listing

`GET /discovery/api/v1/categories/{categoryCode}`

- Purpose: category listing and free-text search with `q`.
- Verified query parameters: `q` and one-based `page`.
- Observed facet keys: `price`, `brands`, `color_palettes`, `attribute_{id}`,
  `has_ready_to_shipment`, `has_ship_by_seller`, `seller_types`,
  `has_offline_shop_stock`, `has_selling_stock`, and `shipment_methods`.
- Observed response data includes `products`, `total_hits`, `filters`,
  `sort_options`, `pager`, `advertisement`, `category`, `breadcrumb`, and
  `seo`.
- The existence of sort IDs was observed; the exact request parameter for
  selecting sort was not captured and must not be assumed.

## Seller listing

`GET /discovery/api/v1/sellers/{sellerCode}`

- Purpose: seller storefront listing.
- Response is listing-shaped and adds a `seller` object with identity, grade,
  rating, statistics, trust/official flags, and description.
- Only one seller was sampled; validate additional sellers before relying on
  every seller field.

## Product detail

`GET /v2/product/{productId}/`

- Purpose: canonical detail source in the supplied research.
- Observed response envelope: `status`, then `data.product` with surrounding
  tracker/SEO/promotion metadata.
- Product fields include identity, images/videos, category/brand, rating,
  specifications, `variants`, comments/questions summaries, breadcrumbs,
  tags, and availability flags.
- Each observed variant includes seller, warranty, price, shipment, status,
  and option/theme data. Price contains selling and RRP prices, stock,
  discount and promotion flags.
- Unavailable products use an empty variants list; code must not expect a
  price/default variant to exist.

## Premium brand

`GET /v1/brands/{code}/premium/`

Observed only once. It returned brand data, product groups, banners, SEO and
menu fields. Do not make it an MVP dependency.

## Explicitly excluded paths

Do not use cart mini, authentication iframe, tracker, feature-flag, or
third-party analytics endpoints. The comments endpoint above was observed
returning 404 and remains unsuitable as a hard dependency.
