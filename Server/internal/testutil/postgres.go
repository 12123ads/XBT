package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// NewPostgres gives each test its own schema, never the connection's public tables.
func NewPostgres(t testing.TB) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("XBT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("XBT_TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	options := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), options)
	if err != nil {
		t.Fatalf("open test postgres: %v", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		adminSQL.Close()
		t.Fatal(err)
	}
	schema := "xbt_test_" + hex.EncodeToString(suffix[:])
	quotedSchema := strconv.Quote(schema)
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		adminSQL.Close()
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE").Error; err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		adminSQL.Close()
	})

	scopedDSN := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("parse test postgres URL: %v", err)
		}
		query := u.Query()
		query.Set("search_path", schema)
		u.RawQuery = query.Encode()
		scopedDSN = u.String()
	}
	database, err := gorm.Open(postgres.Open(scopedDSN), options)
	if err != nil {
		t.Fatalf("open isolated postgres schema: %v", err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { sqlDB.Close() })
	return database, scopedDSN
}
