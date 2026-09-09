package migrations

import (
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const groupModelAllowlistBlueGreenCompatMigration = "234_group_model_allowlist_blue_green_compat.sql"

func TestGroupModelAllowlistBlueGreenCompatMigration(t *testing.T) {
	content, err := FS.ReadFile(groupModelAllowlistBlueGreenCompatMigration)
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "-- blue-green-compatible-before: 235_group_model_allowlist.sql")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb")
	require.Contains(t, sql, "LOCK TABLE groups IN SHARE ROW EXCLUSIVE MODE")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION sync_groups_model_allowlist_blue_green_compat")
	require.Contains(t, sql, "CREATE TRIGGER trg_groups_model_allowlist_blue_green_compat")
	require.NotContains(t, sql, "RENAME COLUMN")

	files, err := fs.Glob(FS, "*.sql")
	require.NoError(t, err)
	sort.Strings(files)
	compatIndex := sort.SearchStrings(files, groupModelAllowlistBlueGreenCompatMigration)
	renameIndex := sort.SearchStrings(files, "235_group_model_allowlist.sql")
	require.Less(t, compatIndex, len(files))
	require.Less(t, renameIndex, len(files))
	require.Less(t, compatIndex, renameIndex)
}
