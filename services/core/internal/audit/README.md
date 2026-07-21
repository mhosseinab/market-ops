# Audit Package

## Objectives
The `audit` package implements the AUD-001 append-only audit trail for the action plane. Its primary objective is to ensure transcript independence: a historical action must be perfectly reproducible from its audit trail alone, without relying on the associated chat conversation.

## How it Works
The package tracks state-changing operations (such as confirmations, revalidation blocks, execution starts, external results, and terminal states). When a state change occurs, an immutable event record is appended. The record captures:
- The actor's identity and role (never inferred from free-text authority).
- The origin surface (e.g., screen, chat, system).
- Evidence versions (APR-001) binding.
- A snapshot of the card state and arbitrary JSON-structured detail about the operation.

## Data Flow
1. **Append**: The `Append` function takes an `Event`, marshals its dynamic JSON payloads (evidence versions, card snapshot, details), and writes an immutable row to the database in the same transaction as the state change.
2. **Reproduce**: The `Reproduce` function queries all records associated with a specific `ActionID`. It retrieves the complete trail needed by a reviewer to reproduce the decision and result, completely bypassing any conversation tables.

## Constraints
- **Append-Only**: There are deliberately no `UPDATE` or `DELETE` paths. The trail is strictly `INSERT` and `SELECT` only.
- **Transcript Independence**: Deleting a conversation must leave the audit reproduction perfectly intact.
- **Identity Enforcement**: The actor must be an identity (principal id/name and role), never a chat message body or free text.

## Data Flow Diagram

```mermaid
flowchart TD
    StateChange([State-Changing Operation]) -->|Generate Event| Event[audit.Event]
    
    Event --> Append[audit.Append]
    
    Append --> Marshal[Marshal JSON:\nEvidence Versions, \nCard Snapshot, \nDetail]
    Marshal --> Insert[db.AppendAuditRecord]
    
    Insert --> DB[(Database:\naudit_records)]
    
    Reviewer([Reviewer / System]) -->|ActionID| Reproduce[audit.Reproduce]
    Reproduce --> Query[db.ListAuditRecordsForAction]
    DB --> Query
    Query --> Repro[audit.Reproduction]
    Repro --> HasTerminal{HasTerminal()?}
```
