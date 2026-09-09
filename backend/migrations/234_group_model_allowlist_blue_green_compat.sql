-- blue-green-compatible-before: 235_group_model_allowlist.sql
--
-- Keep the legacy models_list_config column and the new model_allowlist column
-- synchronized while blue-green slots can run different application versions.
-- This file sorts before 235 so a fresh upgrade expands the schema before the
-- historical rename migration is considered.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Prevent a write from either application version from racing the one-time
-- reconciliation before the synchronizing trigger is installed.
LOCK TABLE groups IN SHARE ROW EXCLUSIVE MODE;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS models_list_config JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS model_allowlist JSONB NOT NULL DEFAULT '{}'::jsonb;

-- model_allowlist is canonical when both versions have written conflicting
-- values. When only one side has a configuration, preserve that value.
UPDATE groups
SET
    model_allowlist = CASE
        WHEN COALESCE(model_allowlist, '{}'::jsonb) = '{}'::jsonb
             AND COALESCE(models_list_config, '{}'::jsonb) <> '{}'::jsonb
            THEN models_list_config
        ELSE COALESCE(model_allowlist, '{}'::jsonb)
    END,
    models_list_config = CASE
        WHEN COALESCE(model_allowlist, '{}'::jsonb) <> '{}'::jsonb
            THEN model_allowlist
        ELSE COALESCE(models_list_config, '{}'::jsonb)
    END
WHERE models_list_config IS NULL
   OR model_allowlist IS NULL
   OR models_list_config IS DISTINCT FROM model_allowlist;

CREATE OR REPLACE FUNCTION sync_groups_model_allowlist_blue_green_compat()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.models_list_config := COALESCE(NEW.models_list_config, '{}'::jsonb);
    NEW.model_allowlist := COALESCE(NEW.model_allowlist, '{}'::jsonb);

    IF TG_OP = 'INSERT' THEN
        IF NEW.model_allowlist <> '{}'::jsonb THEN
            NEW.models_list_config := NEW.model_allowlist;
        ELSE
            NEW.model_allowlist := NEW.models_list_config;
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.model_allowlist IS DISTINCT FROM OLD.model_allowlist
       AND NEW.models_list_config IS NOT DISTINCT FROM OLD.models_list_config THEN
        NEW.models_list_config := NEW.model_allowlist;
    ELSIF NEW.models_list_config IS DISTINCT FROM OLD.models_list_config
       AND NEW.model_allowlist IS NOT DISTINCT FROM OLD.model_allowlist THEN
        NEW.model_allowlist := NEW.models_list_config;
    ELSIF NEW.model_allowlist IS DISTINCT FROM OLD.model_allowlist
       AND NEW.models_list_config IS DISTINCT FROM OLD.models_list_config
       AND NEW.model_allowlist IS DISTINCT FROM NEW.models_list_config THEN
        -- A mixed-version update touched both fields differently; retain the
        -- new field as the canonical value used by the current application.
        NEW.models_list_config := NEW.model_allowlist;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_groups_model_allowlist_blue_green_compat ON groups;

CREATE TRIGGER trg_groups_model_allowlist_blue_green_compat
BEFORE INSERT OR UPDATE OF models_list_config, model_allowlist ON groups
FOR EACH ROW
EXECUTE FUNCTION sync_groups_model_allowlist_blue_green_compat();

COMMENT ON COLUMN groups.models_list_config IS
    'Legacy model-list configuration retained while blue-green slots may run older binaries';

COMMENT ON COLUMN groups.model_allowlist IS
    'Group model allowlist: constrains both model listing responses and request admission';
