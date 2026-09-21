package db

import (
	"encoding/json"
	"fmt"
	"slices"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
)

type indexDescription struct {
	Name             string
	ColumnsJSON      string
	IsUnique         bool
	IsValid          bool
	IsPartial        bool
	HasExpressions   bool
	ConstraintBacked bool
}

func describeIndex(tx *gorm.DB, table, name string) (indexDescription, error) {
	var result indexDescription
	err := tx.Raw(`SELECT idx.relname AS name,
		i.indisunique AS is_unique, i.indisvalid AS is_valid,
		i.indpred IS NOT NULL AS is_partial,
		i.indexprs IS NOT NULL AS has_expressions,
		EXISTS (SELECT 1 FROM pg_constraint c WHERE c.conindid = i.indexrelid) AS constraint_backed,
		(SELECT json_agg(a.attname ORDER BY k.ordinality)::text
		 FROM unnest(i.indkey::smallint[]) WITH ORDINALITY AS k(attnum, ordinality)
		 LEFT JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		 WHERE k.ordinality <= i.indnkeyatts) AS columns_json
		FROM pg_index i JOIN pg_class idx ON idx.oid = i.indexrelid
		WHERE i.indrelid = to_regclass(?) AND idx.relname = ?`, table, name).Scan(&result).Error
	return result, err
}

func indexColumns(raw string) ([]string, error) {
	var columns []string
	if err := json.Unmarshal([]byte(raw), &columns); err != nil {
		return nil, err
	}
	return columns, nil
}

func ensureUniqueIndex(tx *gorm.DB, value any, table, name string, columns []string) error {
	index, err := describeIndex(tx, table, name)
	if err != nil {
		return err
	}
	if index.Name == "" {
		return tx.Migrator().CreateIndex(value, name)
	}
	actual, err := indexColumns(index.ColumnsJSON)
	if err != nil || !index.IsUnique || !index.IsValid || index.IsPartial || index.HasExpressions || index.ConstraintBacked || !slices.Equal(actual, columns) {
		return fmt.Errorf("index %s on %s conflicts with the required unique index", name, table)
	}
	return nil
}

func dropUniqueConstraints(tx *gorm.DB, value any, table string, columns []string) error {
	var constraints []struct {
		Name        string
		ColumnsJSON string
	}
	if err := tx.Raw(`SELECT c.conname AS name,
		(SELECT json_agg(a.attname ORDER BY k.ordinality)::text
		 FROM unnest(c.conkey) WITH ORDINALITY AS k(attnum, ordinality)
		 JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum) AS columns_json
		FROM pg_constraint c WHERE c.conrelid = to_regclass(?) AND c.contype = 'u'`, table).Scan(&constraints).Error; err != nil {
		return err
	}
	for _, constraint := range constraints {
		actual, err := indexColumns(constraint.ColumnsJSON)
		if err != nil {
			return err
		}
		if slices.Equal(actual, columns) {
			if err := tx.Migrator().DropConstraint(value, constraint.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

func prepareLegacySchema(tx *gorm.DB) error {
	for _, spec := range []struct {
		value  any
		table  string
		column string
		index  string
	}{
		{&model.SignShare{}, "sign_shares", "token_hash", "idx_sign_shares_token_hash"},
		{&model.QMXAutoSignAccount{}, "qmx_auto_sign_accounts", "user_uid", "idx_qmx_auto_sign_accounts_user_uid"},
		{&model.VikunjaSettings{}, "vikunja_settings", "user_uid", "idx_vikunja_settings_user_uid"},
	} {
		if !tx.Migrator().HasTable(spec.value) {
			continue
		}
		columns := []string{spec.column}
		if err := ensureUniqueIndex(tx, spec.value, spec.table, spec.index, columns); err != nil {
			return err
		}
		if err := dropUniqueConstraints(tx, spec.value, spec.table, columns); err != nil {
			return err
		}
	}

	if tx.Migrator().HasTable(&model.VikunjaSettings{}) && tx.Migrator().HasColumn(&model.VikunjaSettings{}, "base_url") {
		if tx.Migrator().HasColumn(&model.VikunjaSettings{}, "bound_instance_url") {
			return fmt.Errorf("vikunja settings contain both legacy and bound instance columns; migration requires reconciliation")
		}
		if err := tx.Migrator().RenameColumn(&model.VikunjaSettings{}, "base_url", "bound_instance_url"); err != nil {
			return err
		}
		// The old application could change the URL without changing the token.
		// Its URL is therefore not trustworthy evidence of the token's origin.
		if err := tx.Table("vikunja_settings").Where("1 = 1").Updates(map[string]any{
			"bound_instance_url": "",
			"enabled":            false,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func dropLegacyVikunjaUniqueness(tx *gorm.DB) error {
	if err := ensureUniqueIndex(tx, &model.VikunjaSyncItem{}, "vikunja_sync_items", "idx_vikunja_scope_item", []string{"user_uid", "item_key", "instance_url", "project_id"}); err != nil {
		return err
	}
	columns := []string{"user_uid", "item_key"}
	if err := dropUniqueConstraints(tx, &model.VikunjaSyncItem{}, "vikunja_sync_items", columns); err != nil {
		return err
	}
	index, err := describeIndex(tx, "vikunja_sync_items", "idx_vikunja_user_item")
	if err != nil || index.Name == "" {
		return err
	}
	actual, err := indexColumns(index.ColumnsJSON)
	if err != nil || !index.IsUnique || index.IsPartial || index.HasExpressions || index.ConstraintBacked || !slices.Equal(actual, columns) {
		return fmt.Errorf("legacy Vikunja mapping index has an unexpected definition")
	}
	return tx.Migrator().DropIndex(&model.VikunjaSyncItem{}, index.Name)
}
