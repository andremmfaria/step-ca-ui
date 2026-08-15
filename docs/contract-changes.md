# Contract changes

Every breaking change to the API contract gets a row here, and the `oasdiff`
gate fails a change that lacks one for an operation it reports as broken.

The row is the remedy, deliberately: `/api/v1` is permanent by construction
(5.1), so the alternative remedy considered was bumping an integer no resolver
reads. A sentence a reviewer reads is worth more, and the accumulated rows are
a readable history of every break the contract has taken. See Q5 in
`plans/frontend-backend-split.md`.

This file is append-only. Add new rows at the bottom.

| Operation | What changed | Why |
| --- | --- | --- |
