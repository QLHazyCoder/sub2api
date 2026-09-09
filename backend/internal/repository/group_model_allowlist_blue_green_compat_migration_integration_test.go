//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const groupModelAllowlistBlueGreenCompatMigration = "234_group_model_allowlist_blue_green_compat.sql"
const groupModelAllowlistRenameMigration = "235_group_model_allowlist.sql"

func TestMigration234KeepsOldAndNewGroupModelConfigWritesCompatible(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	// Simulate the pre-235 schema used by the currently serving old slot.
	_, err := tx.ExecContext(ctx, `
DROP TRIGGER IF EXISTS trg_groups_model_allowlist_blue_green_compat ON groups;
ALTER TABLE groups DROP COLUMN model_allowlist;
`)
	require.NoError(t, err)

	var legacyID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, models_list_config)
VALUES ('migration-234-legacy', 'anthropic', 1, 'active', '{"enabled":true,"models":["legacy-model"]}'::jsonb)
RETURNING id
`).Scan(&legacyID))

	applyGroupModelAllowlistBlueGreenCompat(ctx, t, tx)
	requireGroupModelConfigPair(ctx, t, tx, legacyID, `{"enabled":true,"models":["legacy-model"]}`)
	applyGroupModelAllowlistRename(ctx, t, tx)
	requireGroupModelConfigPair(ctx, t, tx, legacyID, `{"enabled":true,"models":["legacy-model"]}`)

	// An old binary writes only models_list_config. The trigger must preserve the
	// write for a concurrently running new binary.
	_, err = tx.ExecContext(ctx, `
UPDATE groups
SET models_list_config = '{"enabled":true,"models":["legacy-write"]}'::jsonb
WHERE id = $1
`, legacyID)
	require.NoError(t, err)
	requireGroupModelConfigPair(ctx, t, tx, legacyID, `{"enabled":true,"models":["legacy-write"]}`)

	// A new binary writes only model_allowlist. The legacy copy must remain
	// usable until the old slot has been fully retired.
	_, err = tx.ExecContext(ctx, `
UPDATE groups
SET model_allowlist = '{"enabled":true,"models":["new-write"]}'::jsonb
WHERE id = $1
`, legacyID)
	require.NoError(t, err)
	requireGroupModelConfigPair(ctx, t, tx, legacyID, `{"enabled":true,"models":["new-write"]}`)

	var newID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, rate_multiplier, status, model_allowlist)
VALUES ('migration-234-new', 'anthropic', 1, 'active', '{"enabled":true,"models":["new-insert"]}'::jsonb)
RETURNING id
`).Scan(&newID))
	requireGroupModelConfigPair(ctx, t, tx, newID, `{"enabled":true,"models":["new-insert"]}`)
}

func applyGroupModelAllowlistBlueGreenCompat(ctx context.Context, t *testing.T, tx *sql.Tx) {
	t.Helper()

	migrationSQL, err := dbmigrations.FS.ReadFile(groupModelAllowlistBlueGreenCompatMigration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
}

func applyGroupModelAllowlistRename(ctx context.Context, t *testing.T, tx *sql.Tx) {
	t.Helper()

	migrationSQL, err := dbmigrations.FS.ReadFile(groupModelAllowlistRenameMigration)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
}

func requireGroupModelConfigPair(ctx context.Context, t *testing.T, tx *sql.Tx, groupID int64, want string) {
	t.Helper()

	var legacy, current string
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT models_list_config::text, model_allowlist::text
FROM groups
WHERE id = $1
`, groupID).Scan(&legacy, &current))
	require.JSONEq(t, want, legacy)
	require.JSONEq(t, want, current)
}
