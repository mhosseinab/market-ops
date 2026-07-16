# Normalization rules

| Concern | Rule | Status |
| --- | --- | --- |
| Unicode | NFC Persian text; normalize ي→ی and ك→ک | Partially verified |
| ZWNJ | preserve for display; normalize in search/diff keys | Verified present |
| Digits | Persian/Arabic-Indic digits→ASCII before parsing | Verified |
| Number separators | strip DOM thousands separators; prefer API integers | Verified |
| Money | store raw Rial as `IRR-rial`; derive Toman as Rial ÷ 10 | Verified |
| Ambiguous money | emit `ambiguous_currency_unit`; do not guess | Rule |
| Dates | Jalali absolute dates→Gregorian ISO; discard relative date | Format verified |
| Direction controls | trim LRM/RLM and do not store them as canonical text | Defensive rule |
| Availability | map `ناموجود` and `out_of_stock` together; do not infer from price | Verified |
| Product URLs | slug-strip to `/product/dkp-{id}/`; drop query | Verified |
| Seller URLs | use displayed seller code URL; retain API lower-case code separately | Partially verified |
| Offer identity | native variant id is the offer identity | Verified sample |
| Media | base URL identifies media; keep observed transformations as variants | Verified |
| Specs | preserve category-specific grouped attributes | Verified in two categories |
| Empty data | retain raw distinction among null, empty string/object/array, and absent field | Verified pattern |
