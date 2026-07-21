# Catalog Package

## Objectives
The `catalog` package is responsible for the idempotent synchronization (initial import and incremental refreshes) of a seller's catalog and owned offers. It manages the canonical entities (Product, Variant, Listing, Owned Offer) and provides the read model for the Products workspace. 

## How it Works
- **Synchronization**: The `Syncer` pulls pages of variants from a `Source` (typically a connector). It upserts entities deterministically keyed by native identifiers and appends raw payload snapshots.
- **Resumability**: Sync runs record their progress (`next_page`). If a run is interrupted, it resumes from exactly where it left off.
- **Telemetry**: A robust telemetry tracker records consecutive sync failure streaks, advancing on faults and resetting on success.
- **Read Model**: Provides paginated access to the canonical products while enforcing capability gating and account isolation.

## Data Flow
1. **Sync Enqueue**: An incremental sync run is transactionally enqueued (tying a River job to a `catalog_sync_run` row).
2. **Fetch and Apply**: The `Syncer` fetches pages. For each variant, it upserts the canonical tables (`products`, `variants`, `listings`, `owned_offers`) and inserts a raw payload snapshot in a single database transaction. 
3. **Reconciliation**: At the end of a sync, drift is calculated to find owned offers that were missing from the latest fetch.
4. **Read Path**: The `ReadService` queries the canonical entities, joins them with identity mapping and market offer data, and evaluates capability flags (e.g., `owned_offer_read`) before returning `ProductRow` views.

## Constraints
- **Money Quarantine**: Owned-offer prices are processed and stored exclusively as raw evidence (`money.RawAmount`). The code path strictly avoids promoting a price to a verified `Money` type to adhere to quarantine rules.
- **Serialization**: Only one non-terminal sync run may exist per marketplace account at a time. This serialization prevents interleaved page writes and corrupted cursors.
- **Idempotency**: Reordering or replaying sync payloads preserves identity and never creates duplicate canonical records.
- **Cross-Account Isolation**: Read operations rigorously fail closed across accounts. An unauthorized request yields an `ErrAccountNotFound` before any data is read.
- **Capability Gating**: The read model gates owned-offer data based on the `owned_offer_read` capability. If the capability is not explicitly supported, no fabricated price or stock data is emitted.

## Data Flow Diagram

```mermaid
flowchart TD
    subgraph Sync Path
        Enq[Enqueue Sync (Initial/Incremental)] -->|Create run row| Tx1[(Tx: catalog_sync_run)]
        Tx1 -->|Success| Job[River Job Enqueued]
        Tx1 -->|Conflict| ErrInflight[ErrSyncAlreadyInFlight]
        
        Job --> Resume[Syncer.Resume]
        Resume --> Fetch[Source.FetchVariantsPage]
        
        Fetch --> Tx2[(Tx: Apply Page)]
        Tx2 --> UpdProd[Upsert Product, Variant,\nListing, Owned Offer]
        Tx2 --> Snap[Insert Payload Snapshot]
        Tx2 --> Adv[Advance Run Cursor]
        
        Adv -->|More pages| Fetch
        Adv -->|Done| Finish[Reconcile Drift]
        Finish --> Complete[(Complete Run)]
    end
    
    subgraph Read Path
        Read[ListProducts / GetProduct] --> Assert[assertOwned]
        Assert -->|No/Foreign| Err404[ErrAccountNotFound]
        Assert -->|Yes| Cap[ownedOfferCapability]
        
        Cap --> Query[Fetch Canonical Row]
        Query --> Mkt[marketOffersByTarget]
        Mkt --> Build[listRowToProductRow / getRowToProductRow]
        Build --> CheckCap{Cap == Supported\n& Present?}
        
        CheckCap -->|Yes| RawPrice[Include Raw Price\n& Stock Evidence]
        CheckCap -->|No| Gated[Gate Data\nUnavailableReason]
        
        RawPrice --> Ret([Return ProductRow])
        Gated --> Ret
    end
```
