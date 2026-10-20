package main

import (
	"context"
	"log"
	"strings"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/captcha"
	"xbt2/server/internal/config"
	"xbt2/server/internal/db"
	"xbt2/server/internal/handler"
	"xbt2/server/internal/im"
	"xbt2/server/internal/middleware"
	"xbt2/server/internal/qmx"
	"xbt2/server/internal/ratelimit"
	"xbt2/server/internal/release"
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

	appReleaseCatalog, appReleaseErr := release.Load(cfg.AppReleaseManifest)
	if appReleaseErr != nil {
		log.Printf("app_release status=unavailable manifest=%s error=%v", cfg.AppReleaseManifest, appReleaseErr)
	} else {
		log.Printf("app_release status=ready manifest=%s package=%s abi=%s version_code=%d apk=%s size=%d sha256=%s",
			cfg.AppReleaseManifest,
			appReleaseCatalog.Manifest.Package,
			appReleaseCatalog.Manifest.ABI,
			appReleaseCatalog.Manifest.Latest.VersionCode,
			appReleaseCatalog.Manifest.Latest.APKFile,
			appReleaseCatalog.SizeBytes,
			appReleaseCatalog.SHA256,
		)
	}

	jwtSvc := service.NewJWTService(cfg.JWTSecret)
	credentialCrypto := service.NewCredentialCrypto(cfg.CredentialSecret)
	xxtClient := xxt.New(cfg.ChaoxingAESKey, cfg.ChaoxingUserAgent, cfg.ChaoxingFID, cfg.AllowInsecureTLS, cfg.ActivityListLimit+1)
	if cfg.CaptchaModelIsEnabled() {
		predictor, err := captcha.New(cfg.CaptchaModelPath)
		if err != nil {
			log.Printf("captcha_model=fallback reason=%v", err)
		} else {
			xxtClient.SetCaptchaPredictor(predictor)
			log.Printf("captcha_model=model path=%s", cfg.CaptchaModelPath)
		}
	} else {
		log.Printf("captcha_model=fallback reason=disabled")
	}

	appReleaseHandler := handler.NewAppReleaseHandler(appReleaseCatalog, cfg.PublicBaseURL)
	authHandler := handler.NewAuthHandler(database, jwtSvc, credentialCrypto, xxtClient)
	facePhotoSvc := service.NewFacePhotoService(database, credentialCrypto, xxtClient)
	authHandler.SetFacePhotoService(facePhotoSvc)
	courseHandler := handler.NewCourseHandler(database, xxtClient, credentialCrypto)
	learningHandler := handler.NewLearningHandler(database, xxtClient, credentialCrypto)
	courseSignWebhook := service.NewEnterpriseWechatWebhookNotifierProvider(runtimeSettingsSvc.CourseSignWebhookURL)
	signSvc := service.NewSignService(database, xxtClient, credentialCrypto, courseSignWebhook)
	signSvc.SetFacePhotoService(facePhotoSvc)
	signSvc.SetLocationJitterMeters(cfg.LocationJitterMeters)
	signSvc.SetFaceDiagnostics(cfg.FaceTransformMild, cfg.FaceUploadMode)
	if cfg.FaceTransformMild || cfg.FaceUploadMode != "face_temp" {
		log.Printf("face_diagnostics enabled transform_mild=%t upload_mode=%s (temporary troubleshooting only)", cfg.FaceTransformMild, cfg.FaceUploadMode)
	}
	signHandler := handler.NewSignHandler(database, xxtClient, credentialCrypto, signSvc, cfg.ActivityListLimit, runtimeSettingsSvc)
	signHandler.SetSignLimiter(ratelimit.New(cfg.SignRateLimitPerMinute, cfg.SignRateLimitBurst, cfg.SignMaxConcurrent))
	imClient := im.New(cfg.IMTokenURL, cfg.IMAPIBaseURL, cfg.IMMessageBaseURL)
	imSignSvc := service.NewIMSignService(database, xxtClient, imClient, credentialCrypto, cfg.IMMinFetchInterval(), cfg.IMFetchTimeout(), cfg.IMEnabled())
	signHandler.SetIMSignSource(imSignSvc)
	whitelistHandler := handler.NewWhitelistHandler(database)
	adminAccountHandler := handler.NewAdminAccountHandler(database, xxtClient, credentialCrypto)
	adminSettingsHandler := handler.NewAdminSettingsHandler(runtimeSettingsSvc)
	qmxClient := qmx.New(cfg.AllowInsecureTLS)
	qmxRoomCheckHandler := handler.NewQMXRoomCheckHandler(qmxClient, database, xxtClient, credentialCrypto)
	qmxAutoSignWebhook := service.NewEnterpriseWechatWebhookNotifierProvider(runtimeSettingsSvc.QMXAutoSignWebhookURL)
	qmxAutoSignSvc := service.NewQMXAutoSignService(database, qmxClient, xxtClient, credentialCrypto, cfg.QMXLocationPresets, qmxAutoSignWebhook)
	qmxAutoSignSvc.SetPresetsProvider(runtimeSettingsSvc.QMXLocationPresets)
	qmxAutoSignHandler := handler.NewAdminQMXAutoSignHandler(database, qmxAutoSignSvc)
	vikunjaSyncSvc, err := service.NewVikunjaSyncService(database, xxtClient, credentialCrypto, cfg.VikunjaBaseURL)
	if err != nil {
		log.Fatalf("vikunja sync init failed: %v", err)
	}
	vikunjaHandler := handler.NewVikunjaHandler(database, credentialCrypto, vikunjaSyncSvc, cfg.VikunjaBaseURL)

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
		api.GET("/app/version", appReleaseHandler.Version)
		api.GET("/app/download/:file", appReleaseHandler.Download)

		authed := api.Group("")
		authed.Use(middleware.Auth(jwtSvc, database))
		{
			authed.GET("/user/face", authHandler.GetFace)
			authed.POST("/user/face", authHandler.UploadFace)
			authed.DELETE("/user/face", authHandler.DeleteFace)
			authed.GET("/user/faces", authHandler.ListFaces)
			authed.POST("/user/faces", authHandler.AddFace)
			authed.DELETE("/user/faces/:id", authHandler.DeleteFaceByID)
			authed.POST("/user/faces/import", authHandler.ImportFace)

			authed.GET("/courses", courseHandler.List)
			authed.POST("/courses/sync", courseHandler.Sync)
			authed.PUT("/courses/selection", courseHandler.UpdateSelection)
			authed.GET("/learning/dashboard", learningHandler.Dashboard)
			authed.GET("/learning/captcha", learningHandler.CaptchaImage)
			authed.POST("/learning/captcha", learningHandler.CaptchaSubmit)
			authed.GET("/vikunja/settings", vikunjaHandler.Settings)
			authed.PUT("/vikunja/settings", vikunjaHandler.UpdateSettings)
			authed.POST("/vikunja/test", vikunjaHandler.TestConnection)
			authed.POST("/vikunja/sync", vikunjaHandler.SyncNow)

			authed.GET("/features", signHandler.Features)
			authed.GET("/class-admin/overview", adminAccountHandler.ClassAdminOverview)
			authed.POST("/class-admin/accounts", adminAccountHandler.CreateClassAccount)
			authed.GET("/sign/activities", signHandler.Activities)
			authed.GET("/sign/im/status", signHandler.IMStatus)
			authed.GET("/sign/classmates", signHandler.Classmates)
			authed.POST("/sign/check", signHandler.Check)
			authed.POST("/sign/execute", signHandler.Execute)
			authed.POST("/sign/sign-out", signHandler.SignOut)
			authed.POST("/sign/photo", signHandler.Photo)
			authed.POST("/sign/shares", signHandler.CreateShare)
			authed.GET("/sign/contributions", signHandler.Contributions)
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
				admin.GET("/sign-metrics", signHandler.SignMetrics)
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
	vikunjaSyncSvc.StartScheduler(context.Background())
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
