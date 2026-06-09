package main

import (
	"context"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/config"
	"xbt2/server/internal/db"
	"xbt2/server/internal/handler"
	"xbt2/server/internal/middleware"
	"xbt2/server/internal/qmx"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

func main() {
	cfg := config.Load()
	gin.SetMode(resolveGinMode(cfg.AppEnv))

	database, err := db.New(cfg)
	if err != nil {
		log.Fatalf("db init failed: %v", err)
	}
	runtimeSettingsSvc := service.NewRuntimeSettingsService(database, cfg)

	jwtSvc := service.NewJWTService(cfg.JWTSecret)
	credentialCrypto := service.NewCredentialCrypto(cfg.CredentialSecret)
	xxtClient := xxt.New(cfg.ChaoxingAESKey, cfg.ChaoxingUserAgent, cfg.AllowInsecureTLS, cfg.ActivityListLimit+1)

	authHandler := handler.NewAuthHandler(database, jwtSvc, credentialCrypto, xxtClient)
	courseHandler := handler.NewCourseHandler(database, xxtClient, credentialCrypto)
	courseSignWebhook := service.NewEnterpriseWechatWebhookNotifierProvider(runtimeSettingsSvc.CourseSignWebhookURL)
	signSvc := service.NewSignService(database, xxtClient, credentialCrypto, courseSignWebhook)
	signHandler := handler.NewSignHandler(database, xxtClient, credentialCrypto, signSvc, cfg.ActivityListLimit, runtimeSettingsSvc)
	whitelistHandler := handler.NewWhitelistHandler(database)
	adminAccountHandler := handler.NewAdminAccountHandler(database, xxtClient, credentialCrypto)
	adminSettingsHandler := handler.NewAdminSettingsHandler(runtimeSettingsSvc)
	qmxClient := qmx.New(cfg.AllowInsecureTLS)
	qmxRoomCheckHandler := handler.NewQMXRoomCheckHandler(qmxClient, database, xxtClient, credentialCrypto)
	qmxAutoSignWebhook := service.NewEnterpriseWechatWebhookNotifierProvider(runtimeSettingsSvc.QMXAutoSignWebhookURL)
	qmxAutoSignSvc := service.NewQMXAutoSignService(database, qmxClient, xxtClient, credentialCrypto, cfg.QMXLocationPresets, qmxAutoSignWebhook)
	qmxAutoSignSvc.SetPresetsProvider(runtimeSettingsSvc.QMXLocationPresets)
	qmxAutoSignHandler := handler.NewAdminQMXAutoSignHandler(database, qmxAutoSignSvc)

	r := gin.Default()

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"code": 0, "message": "ok", "data": gin.H{"service": "xbt2-server"}})
		})
		api.POST("/auth/login", authHandler.Login)
		api.GET("/sign/location-presets", signHandler.LocationPresets)
		api.GET("/sign/shares/:token", signHandler.GetShare)
		api.POST("/sign/shares/:token/execute", signHandler.ExecuteShare)

		authed := api.Group("")
		authed.Use(middleware.Auth(jwtSvc))
		{
			authed.GET("/courses", courseHandler.List)
			authed.POST("/courses/sync", courseHandler.Sync)
			authed.PUT("/courses/selection", courseHandler.UpdateSelection)

			authed.GET("/sign/activities", signHandler.Activities)
			authed.GET("/sign/classmates", signHandler.Classmates)
			authed.POST("/sign/check", signHandler.Check)
			authed.POST("/sign/execute", signHandler.Execute)
			authed.POST("/sign/shares", signHandler.CreateShare)
			authed.POST("/qmx/room-check/preview", qmxRoomCheckHandler.Preview)
			authed.POST("/qmx/room-check/execute", qmxRoomCheckHandler.Execute)
			authed.GET("/qmx/auto-sign/settings", qmxAutoSignHandler.GetOwnSettings)
			authed.PUT("/qmx/auto-sign/settings", qmxAutoSignHandler.UpdateOwnSettings)
			authed.POST("/qmx/auto-sign/locations/preview", qmxAutoSignHandler.PreviewOwnLocations)
			authed.POST("/qmx/auto-sign/run", qmxAutoSignHandler.RunOwnAccount)

			admin := authed.Group("/admin")
			admin.Use(middleware.AdminOnly())
			{
				admin.GET("/whitelist/users", whitelistHandler.ListUsers)
				admin.POST("/whitelist/users", whitelistHandler.CreateUser)
				admin.POST("/whitelist/users/import", whitelistHandler.BatchImportUsers)
				admin.DELETE("/whitelist/users/:id", whitelistHandler.DeleteUser)

				admin.GET("/accounts", adminAccountHandler.ListAccounts)
				admin.POST("/accounts", adminAccountHandler.CreateAccount)
				admin.GET("/sign-records", adminAccountHandler.ListSignRecords)
				admin.GET("/settings", adminSettingsHandler.Get)
				admin.PUT("/settings", adminSettingsHandler.Update)
				admin.GET("/accounts/:uid/courses", adminAccountHandler.ListUserCourses)
				admin.POST("/accounts/:uid/courses", adminAccountHandler.AddUserCourse)
				admin.POST("/accounts/:uid/courses/sync", adminAccountHandler.SyncUserCourses)
				admin.PUT("/accounts/:uid/courses/selection", adminAccountHandler.UpdateUserCourseSelection)
				admin.POST("/courses/copy-selection", adminAccountHandler.CopySelectedCourses)

				admin.GET("/class-groups", adminAccountHandler.ListClassGroups)
				admin.POST("/class-groups", adminAccountHandler.CreateClassGroup)
				admin.PUT("/class-groups/:id", adminAccountHandler.UpdateClassGroup)
				admin.DELETE("/class-groups/:id", adminAccountHandler.DeleteClassGroup)
				admin.PUT("/class-groups/:id/members", adminAccountHandler.UpdateClassGroupMembers)
				admin.POST("/class-groups/:id/courses/copy-selection", adminAccountHandler.CopyClassGroupSelectedCourses)

				admin.GET("/qmx-auto-sign", qmxAutoSignHandler.Overview)
				admin.PUT("/qmx-auto-sign/settings", qmxAutoSignHandler.UpdateSettings)
				admin.POST("/qmx-auto-sign/accounts/:uid/locations/preview", qmxAutoSignHandler.PreviewLocations)
				admin.PUT("/qmx-auto-sign/accounts/:uid", qmxAutoSignHandler.UpdateAccount)
				admin.POST("/qmx-auto-sign/accounts/:uid/run", qmxAutoSignHandler.RunAccount)
				admin.GET("/qmx-auto-sign/records", qmxAutoSignHandler.Records)
			}
		}
	}

	qmxAutoSignSvc.StartScheduler(context.Background())
	log.Printf("xbt2 server listening on %s (app_env=%s, gin_mode=%s)", cfg.HTTPAddr, cfg.AppEnv, gin.Mode())
	if err := r.Run(cfg.HTTPAddr); err != nil {
		log.Fatalf("server start failed: %v", err)
	}
}

func resolveGinMode(appEnv string) string {
	switch strings.ToLower(strings.TrimSpace(appEnv)) {
	case "prod", "production":
		return gin.ReleaseMode
	case "test", "testing":
		return gin.TestMode
	case "dev", "development":
		fallthrough
	default:
		return gin.DebugMode
	}
}
