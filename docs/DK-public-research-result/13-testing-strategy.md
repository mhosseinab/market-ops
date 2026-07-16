# Testing strategy

| Layer | Target | Expected check |
| --- | --- | --- |
| Unit | normalization | digits, Jalali dates, money unit, ZWNJ |
| Unit | page classification | product/search/seller/redirect URL fixtures |
| Fixture regression | product extractor | normalized snapshot remains stable until intentional update |
| API contract | verified endpoints | infrequent live canary validates shape/type changes |
| DOM snapshot | selector rules | minimal stored fragments still extract expected state |
| Integration | workflows A/B/C | fixed public samples; diff shape, not changing values |
| Drift detection | top-level response keys | alert on additions/removals; review removals urgently |
| Manual spot check | multi-seller, unavailable, discounted product and seller | repeat documented verification steps quarterly |

The source refers to `file 16` for manual verification steps, but that file was
not supplied. The final row therefore cannot link to a generated document.
