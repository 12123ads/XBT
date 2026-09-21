package db

import (
	"fmt"
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"xbt2/server/internal/config"
	"xbt2/server/internal/model"
)

func New(cfg config.Config) (*gorm.DB, error) {
	newLogger := gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			LogLevel:                  gormlogger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(postgres.Open(cfg.PostgresDSN), &gorm.Config{
		Logger: newLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres failed: %w", err)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := prepareLegacySchema(tx); err != nil {
			return err
		}
		if err := tx.AutoMigrate(
			&model.User{},
			&model.Whitelist{},
			&model.Course{},
			&model.UserCourse{},
			&model.ClassGroup{},
			&model.ClassGroupMember{},
			&model.AppSetting{},
			&model.SignActivity{},
			&model.SignActivityScope{},
			&model.SignShare{},
			&model.SignRecord{},
			&model.QMXAutoSignSetting{},
			&model.QMXAutoSignAccount{},
			&model.QMXAutoSignRecord{},
			&model.QMXAutoSignRunState{},
			&model.VikunjaSettings{},
			&model.VikunjaSyncItem{},
		); err != nil {
			return err
		}
		return dropLegacyVikunjaUniqueness(tx)
	}); err != nil {
		if sqlDB, closeErr := db.DB(); closeErr == nil {
			sqlDB.Close()
		}
		return nil, fmt.Errorf("auto migrate failed: %w", err)
	}

	return db, nil
}
