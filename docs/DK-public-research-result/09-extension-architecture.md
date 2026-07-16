# Extension architecture

## MVP

Use Manifest V3 with a content script and service worker. A page-context script
is not required for MVP: content-script fetches can retrieve the verified
public product/search/seller endpoints while the user is on the active tab.
Page-context interception is diagnostic-only, capability-gated, and never
modifies page traffic.

## Responsibilities

- Content script: classify page, fetch verified endpoint, normalize response,
  redact before sending, scope `MutationObserver` to relevant tab panel, and
  detect SPA navigation through history events plus `popstate`.
- Service worker: queue in `chrome.storage.local`, batch uploads, retry with
  bounded backoff, enforce queue cap with a metric, and compute PII-redacted
  content hashes for idempotency.

## Permissions

| Permission | Purpose |
| --- | --- |
| `activeTab` | operate only on an active Digikala tab |
| hosts for `www.digikala.com` and `api.digikala.com` | injection and API fetch |
| `storage` | local queue/config |
| `alarms` | queue flush and low-frequency self-check |
| `scripting` | optional diagnostic interception only |

Do not request `<all_urls>`, `webRequest`, or `webRequestBlocking`.

Upload batches contain crawl run/version metadata and observations. The backend
must deduplicate the batch content hash. Do not use alarms for autonomous
whole-site browsing; self-check only a small fixed public canary set. Resolve
lazy home widgets only after visible user scroll intent.
