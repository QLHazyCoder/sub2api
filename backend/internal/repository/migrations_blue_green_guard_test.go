package repository

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestBlueGreenUnsafeMigrationReason(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		unsafe bool
		reason string
	}{
		{
			name:   "rename column",
			sql:    "ALTER TABLE groups RENAME COLUMN models_list_config TO model_allowlist;",
			unsafe: true,
			reason: "renames a table or column",
		},
		{
			name:   "drop column",
			sql:    "ALTER TABLE groups DROP COLUMN models_list_config;",
			unsafe: true,
			reason: "drops a table, column, type, schema, or constraint",
		},
		{
			name:   "additive column",
			sql:    "ALTER TABLE groups ADD COLUMN IF NOT EXISTS model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb;",
			unsafe: false,
		},
		{
			name:   "required column without default",
			sql:    "ALTER TABLE groups ADD COLUMN required_flag BOOLEAN NOT NULL;",
			unsafe: true,
			reason: "adds a required column without a default for writes from the active slot",
		},
		{
			name:   "comment does not trigger guard",
			sql:    "-- ALTER TABLE groups RENAME COLUMN old TO new\nALTER TABLE groups ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT false;",
			unsafe: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, unsafe := blueGreenUnsafeMigrationReason(test.sql)
			require.Equal(t, test.unsafe, unsafe)
			if test.unsafe {
				require.Equal(t, test.reason, reason)
			}
		})
	}
}

func TestApplyMigrationsFS_BlueGreenSchemaGuardBlocksUnsafePendingMigration(t *testing.T) {
	t.Setenv(blueGreenSchemaGuardEnv, "true")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	prepareMigrationsBootstrapExpectations(mock)
	mock.ExpectQuery("SELECT checksum FROM schema_migrations WHERE filename = \\$1").
		WithArgs("002_rename.sql").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("SELECT pg_advisory_unlock\\(\\$1\\)").
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fsys := fstest.MapFS{
		"001_expand.sql": &fstest.MapFile{Data: []byte("ALTER TABLE groups ADD COLUMN new_name TEXT;")},
		"002_rename.sql": &fstest.MapFile{Data: []byte("ALTER TABLE groups RENAME COLUMN old_name TO new_name;")},
	}

	err = applyMigrationsFS(context.Background(), db, fsys)
	require.Error(t, err)
	require.Contains(t, err.Error(), "blue-green schema guard blocked pending migration 002_rename.sql")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBlueGreenCompatibilityTargetsRequireAnEarlierMigration(t *testing.T) {
	targets := blueGreenCompatibilityTargets("-- blue-green-compatible-before: 235_group_model_allowlist.sql\n")
	require.Equal(t, []string{"235_group_model_allowlist.sql"}, targets)
}

func TestBlueGreenSchemaGuardAcceptsEarlierDeclaredCompatibility(t *testing.T) {
	t.Setenv(blueGreenSchemaGuardEnv, "true")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	fsys := fstest.MapFS{
		"234_expand.sql": &fstest.MapFile{Data: []byte(`
-- blue-green-compatible-before: 235_rename.sql
ALTER TABLE groups ADD COLUMN IF NOT EXISTS new_name TEXT;
`)},
		"235_rename.sql": &fstest.MapFile{Data: []byte(`
ALTER TABLE groups RENAME COLUMN old_name TO new_name;
`)},
	}

	err = validateBlueGreenMigrationPlan(context.Background(), db, fsys, []string{"234_expand.sql", "235_rename.sql"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
