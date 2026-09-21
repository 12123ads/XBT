package db_test

import (
	"os"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/config"
	"xbt2/server/internal/db"
	"xbt2/server/internal/model"
	"xbt2/server/internal/testutil"
)

func migrate(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	database, err := db.New(config.Config{PostgresDSN: dsn})
	if err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return database
}

func executeFile(t *testing.T, database *gorm.DB, path string) {
	t.Helper()
	ddl, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(string(ddl)).Error; err != nil {
		t.Fatalf("execute schema %s: %v", path, err)
	}
}

func TestLegacySchemaUpgradePreservesDataAndRequiresRebinding(t *testing.T) {
	raw, dsn := testutil.NewPostgres(t)
	executeFile(t, raw, "testdata/pre_fix_init.sql")
	if err := raw.Exec(`INSERT INTO users (uid,mobile,name,credential_cipher) VALUES (101,'18800000101','Existing User','preserve-user-cipher');
		INSERT INTO vikunja_settings (user_uid,base_url,api_token_cipher,project_id,enabled)
		VALUES (101,'https://old.example','preserve-token-cipher',20,true);
		INSERT INTO vikunja_sync_items (user_uid,item_key,vikunja_task_id,project_id,title)
		VALUES (101,'homework:one',99,20,'Existing Homework')`).Error; err != nil {
		t.Fatal(err)
	}
	database := migrate(t, dsn)
	var user model.User
	if err := database.Where("uid = ?", 101).First(&user).Error; err != nil || user.CredentialCipher != "preserve-user-cipher" {
		t.Fatalf("user credential changed during migration: %v", err)
	}
	var settings model.VikunjaSettings
	if err := database.Where("user_uid = ?", 101).First(&settings).Error; err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || settings.BoundInstanceURL != "" || settings.APITokenCipher != "preserve-token-cipher" {
		t.Fatal("legacy token must be retained but unbound and disabled")
	}
	var oldMapping model.VikunjaSyncItem
	if err := database.Where("vikunja_task_id = ?", 99).First(&oldMapping).Error; err != nil {
		t.Fatal(err)
	}
	if oldMapping.InstanceURL != "" || oldMapping.Title != "Existing Homework" {
		t.Fatal("legacy mapping was lost or assigned an unproven instance")
	}
	if database.Migrator().HasColumn(&model.VikunjaSettings{}, "base_url") {
		t.Fatal("legacy writable destination column remains")
	}
	if err := database.Model(&settings).Updates(map[string]any{"bound_instance_url": "https://new.example", "enabled": true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, entry := range []model.VikunjaSyncItem{
		{UserUID: 101, ItemKey: "homework:one", InstanceURL: "https://new.example", ProjectID: 20, VikunjaTaskID: 100},
		{UserUID: 101, ItemKey: "homework:one", InstanceURL: "https://new.example", ProjectID: 21, VikunjaTaskID: 101},
		{UserUID: 101, ItemKey: "homework:one", InstanceURL: "https://other.example", ProjectID: 20, VikunjaTaskID: 102},
	} {
		if err := database.Create(&entry).Error; err != nil {
			t.Fatalf("independent mapping namespace rejected: %v", err)
		}
	}
	duplicate := model.VikunjaSyncItem{UserUID: 101, ItemKey: "homework:one", InstanceURL: "https://new.example", ProjectID: 20, VikunjaTaskID: 999}
	if err := database.Create(&duplicate).Error; err == nil {
		t.Fatal("duplicate source item in one destination was accepted")
	}
	database = migrate(t, dsn)
	var rebound model.VikunjaSettings
	if err := database.Where("user_uid = ?", 101).First(&rebound).Error; err != nil || !rebound.Enabled || rebound.BoundInstanceURL != "https://new.example" {
		t.Fatalf("second startup invalidated a new binding: %v", err)
	}
	var legacy model.VikunjaSyncItem
	if err := database.Where("vikunja_task_id = ?", 99).First(&legacy).Error; err != nil || legacy.InstanceURL != "" {
		t.Fatalf("second startup rebound the quarantined mapping: %v", err)
	}
}

func TestFreshAndGORMDatabasesRestartWithUniqueScopes(t *testing.T) {
	for _, schema := range []string{"init.sql", "gorm"} {
		t.Run(schema, func(t *testing.T) {
			raw, dsn := testutil.NewPostgres(t)
			if schema == "init.sql" {
				executeFile(t, raw, "../../init.sql")
			} else if err := raw.AutoMigrate(&model.User{}, &model.SignShare{}, &model.QMXAutoSignAccount{}, &model.VikunjaSettings{}, &model.VikunjaSyncItem{}); err != nil {
				t.Fatal(err)
			}
			database := migrate(t, dsn)
			if err := database.Create(&model.SignActivityScope{ActivityID: 900, CourseID: 10, ClassID: 20}).Error; err != nil {
				t.Fatal(err)
			}
			if err := database.Create(&model.SignActivityScope{ActivityID: 900, CourseID: 10, ClassID: 21}).Error; err != nil {
				t.Fatalf("one upstream activity may be visible to multiple classes: %v", err)
			}
			database = migrate(t, dsn)
			var count int64
			if err := database.Model(&model.SignActivityScope{}).Where("activity_id = ?", 900).Count(&count).Error; err != nil || count != 2 {
				t.Fatalf("activity associations did not survive restart: count=%d err=%v", count, err)
			}
			if err := database.Create(&model.SignActivityScope{ActivityID: 900, CourseID: 10, ClassID: 20}).Error; err == nil {
				t.Fatal("duplicate activity association accepted")
			}
		})
	}
}

func TestMigrationConflictRollsBackLegacyCredentialChanges(t *testing.T) {
	raw, dsn := testutil.NewPostgres(t)
	executeFile(t, raw, "testdata/pre_fix_init.sql")
	if err := raw.Exec(`INSERT INTO vikunja_settings (user_uid,base_url,api_token_cipher,project_id,enabled)
		VALUES (101,'https://old.example','unchanged-cipher',20,true);
		CREATE UNIQUE INDEX idx_vikunja_scope_item ON vikunja_sync_items (user_uid)`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := db.New(config.Config{PostgresDSN: dsn}); err == nil {
		t.Fatal("conflicting destination index must block migration")
	}
	if !raw.Migrator().HasColumn("vikunja_settings", "base_url") || raw.Migrator().HasColumn("vikunja_settings", "bound_instance_url") {
		t.Fatal("failed migration partially changed the legacy binding schema")
	}
	var row struct {
		BaseURL        string
		APITokenCipher string
		Enabled        bool
	}
	if err := raw.Table("vikunja_settings").Where("user_uid = ?", 101).Take(&row).Error; err != nil || row.BaseURL != "https://old.example" || row.APITokenCipher != "unchanged-cipher" || !row.Enabled {
		t.Fatalf("failed migration modified legacy configuration: %v", err)
	}
	var constraints int64
	if err := raw.Raw(`SELECT count(*) FROM pg_constraint WHERE conrelid=to_regclass('sign_shares') AND conname='sign_shares_token_hash_key'`).Scan(&constraints).Error; err != nil || constraints != 1 {
		t.Fatalf("legacy uniqueness was not rolled back: count=%d err=%v", constraints, err)
	}
}
