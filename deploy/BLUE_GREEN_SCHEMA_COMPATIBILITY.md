# Blue-Green Shared Schema Compatibility

Sub2API blue-green slots share PostgreSQL, Redis, and `/app/data`. A healthy
standby is not sufficient evidence that the active slot remains compatible
with a schema migration performed by that standby.

## Required release sequence

1. Treat a renamed, dropped, type-changed, or newly constrained field as an
   expand-migrate-contract change, not a one-release migration.
2. First release only additive schema and compatibility code. When two binary
   versions must coexist, retain old columns and keep old/new representations
   synchronized.
3. Start the standby only after its pending migration plan passes the
   blue-green schema guard. The guard is enabled by
   `SUB2API_BLUE_GREEN_SCHEMA_GUARD=true` in the local blue-green Compose
   services.
4. Verify the standby and the active slot both continue to execute their real
   database-backed workflows. `/health` alone does not validate this.
5. Only after every running slot uses the expanded schema may a later,
   separately reviewed maintenance release remove the legacy representation.

## Guard behavior

Before applying any pending migration, the guard blocks a blue-green standby
when it finds a table/column rename, destructive drop, type or nullability
change, write-rejecting table constraint, truncate, or delete. No pending
migration runs after that failure.

An exception must be explicit: an earlier migration can declare
`-- blue-green-compatible-before: target_migration.sql` after it has been
reviewed to make that target safe. The declaration must sort before its target.
This is intended for a proven expand step, not as a bypass for unsafe changes.
