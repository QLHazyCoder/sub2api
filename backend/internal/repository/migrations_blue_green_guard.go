package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const blueGreenSchemaGuardEnv = "SUB2API_BLUE_GREEN_SCHEMA_GUARD"

const blueGreenCompatibleBeforePrefix = "-- blue-green-compatible-before:"

type blueGreenUnsafeMigrationPattern struct {
	reason string
	re     *regexp.Regexp
}

var blueGreenUnsafeMigrationPatterns = []blueGreenUnsafeMigrationPattern{
	{
		reason: "renames a table or column",
		re:     regexp.MustCompile(`(?is)\balter\s+table\b[^;]*\brename\s+(?:column\b|to\b)`),
	},
	{
		reason: "drops a table, column, type, schema, or constraint",
		re:     regexp.MustCompile(`(?is)\bdrop\s+(?:table|column|type|schema|constraint)\b`),
	},
	{
		reason: "changes a column type or nullability",
		re:     regexp.MustCompile(`(?is)\balter\s+table\b[^;]*\balter\s+column\b[^;]*\b(?:type\b|set\s+not\s+null\b|drop\s+not\s+null\b)`),
	},
	{
		reason: "adds a table constraint that can reject writes from the active slot",
		re:     regexp.MustCompile(`(?is)\balter\s+table\b[^;]*\badd\s+constraint\b`),
	},
	{
		reason: "truncates or deletes persisted data",
		re:     regexp.MustCompile(`(?is)\b(?:truncate(?:\s+table)?|delete\s+from)\b`),
	},
}

var blueGreenBlockCommentPattern = regexp.MustCompile(`(?s)/\*.*?\*/`)
var blueGreenAddColumnPattern = regexp.MustCompile(`(?is)\balter\s+table\b[^;]*\badd\s+column\b([^;]*)`)

// blueGreenSchemaGuardEnabled is intentionally opt-in. Normal single-instance
// installations can retain upstream migration behavior, while the local
// blue-green Compose configuration enables the stricter shared-database gate.
func blueGreenSchemaGuardEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(blueGreenSchemaGuardEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// validateBlueGreenMigrationPlan runs before any pending migration is applied.
// A standby must not be allowed to change a shared schema in a way that makes
// the still-serving slot unable to read or write. Explicit compatibility
// declarations are only accepted from an earlier migration in the same plan.
func validateBlueGreenMigrationPlan(ctx context.Context, db migrationConnection, fsys fs.FS, files []string) error {
	if !blueGreenSchemaGuardEnabled() {
		return nil
	}

	positions := make(map[string]int, len(files))
	for index, name := range files {
		positions[name] = index
	}

	compatibilityDeclarations := make(map[string]string)
	unsafeMigrations := make(map[string]string)
	for index, name := range files {
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("read migration %s for blue-green guard: %w", name, err)
		}

		for _, target := range blueGreenCompatibilityTargets(string(content)) {
			targetIndex, exists := positions[target]
			if !exists {
				return fmt.Errorf("blue-green compatibility declaration in %s references unknown migration %s", name, target)
			}
			if index >= targetIndex {
				return fmt.Errorf("blue-green compatibility declaration in %s must precede %s", name, target)
			}
			compatibilityDeclarations[target] = name
		}

		if reason, unsafe := blueGreenUnsafeMigrationReason(string(content)); unsafe {
			unsafeMigrations[name] = reason
		}
	}

	for _, name := range files {
		reason, unsafe := unsafeMigrations[name]
		if !unsafe {
			continue
		}
		if _, declaredCompatible := compatibilityDeclarations[name]; declaredCompatible {
			continue
		}

		var checksum string
		err := db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename = $1", name).Scan(&checksum)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check blue-green migration guard for %s: %w", name, err)
		}

		return fmt.Errorf(
			"blue-green schema guard blocked pending migration %s because it %s; "+
				"add and verify an earlier expand-compatible migration before starting a standby, "+
				"or use an explicitly approved maintenance window after all old slots are stopped",
			name,
			reason,
		)
	}

	return nil
}

func blueGreenCompatibilityTargets(content string) []string {
	var targets []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(trimmed), blueGreenCompatibleBeforePrefix) {
			continue
		}
		if target := strings.TrimSpace(trimmed[len(blueGreenCompatibleBeforePrefix):]); target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}

func blueGreenUnsafeMigrationReason(content string) (string, bool) {
	executableSQL := blueGreenBlockCommentPattern.ReplaceAllString(content, " ")
	lines := strings.Split(executableSQL, "\n")
	for index, line := range lines {
		if commentIndex := strings.Index(line, "--"); commentIndex >= 0 {
			lines[index] = line[:commentIndex]
		}
	}
	executableSQL = strings.Join(lines, "\n")

	for _, pattern := range blueGreenUnsafeMigrationPatterns {
		if pattern.re.MatchString(executableSQL) {
			return pattern.reason, true
		}
	}
	for _, match := range blueGreenAddColumnPattern.FindAllStringSubmatch(executableSQL, -1) {
		definition := strings.ToLower(match[1])
		if strings.Contains(definition, "not null") && !strings.Contains(definition, "default") {
			return "adds a required column without a default for writes from the active slot", true
		}
	}
	return "", false
}
