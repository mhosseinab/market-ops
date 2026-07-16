# Data source inventory

| Source | Class | Guest session | Confidence | Connector use |
| --- | --- | --- | --- | --- |
| `GET /discovery/api/v1/home` | B | Yes | High | Homepage discovery only |
| `GET /discovery/api/v1/widget-factory/widget/{id}` | B | Yes | High | Resolve lazy homepage widgets |
| `GET /discovery/api/v1/categories/{code}` | B | Yes | High | Primary search/category listing |
| `GET /discovery/api/v1/sellers/{code}` | B | Yes | Partially verified | Seller listing and aggregate stats |
| `GET /v2/product/{id}/` | B | Yes | High | Primary product/offer detail |
| `GET /v1/brands/{code}/premium/` | B | Yes | Low confidence | Premium brands only; not MVP-critical |
| `GET /v1/user/init/` | E | Guest cookie | High | Branch session behaviour only |
| `GET /v1/rate-and-review/products/{id}/comments/` | B | Unknown | Low | Do not depend on it |
| `__NEXT_DATA__` | C | n/a | Empty in samples | Do not use |
| JSON-LD | C | n/a | Absent in samples | Do not use |
| Rendered DOM | D | n/a | Verified | Fallback/validation only |
| Cart, auth, tracking, flags | F | n/a | n/a | Excluded |

Class B means undocumented web endpoint; C means embedded structured data; D
means rendered DOM; E means session-dependent; F means unsuitable or
security/privacy-sensitive for this connector.

## Required handling

- Do not retain address, city, cart, authentication, tracking, or feature-flag
  data.
- Drop/redact reviewer and question-sender identities.
- Treat endpoint and field changes as integration failures requiring review.
