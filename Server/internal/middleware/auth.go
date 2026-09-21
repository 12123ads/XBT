package middleware

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/service"
)

func Auth(jwtSvc *service.JWTService, database *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			common.Fail(c, 401, "unauthorized")
			c.Abort()
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer"))
		claims, err := jwtSvc.Parse(token)
		if err != nil {
			common.Fail(c, 401, "invalid token")
			c.Abort()
			return
		}
		user, err := service.LoadActiveUser(database.WithContext(c.Request.Context()), claims.UID)
		if err != nil {
			if errors.Is(err, service.ErrAccountInactive) {
				common.Fail(c, 401, "account inactive")
			} else {
				common.Fail(c, 500, "query account failed")
			}
			c.Abort()
			return
		}
		c.Set(common.CtxUserUID, user.UID)
		c.Set(common.CtxMobile, user.Mobile)
		c.Set(common.CtxPermission, user.Permission)
		c.Next()
	}
}

func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.GetPermission(c) < 2 {
			common.Fail(c, 403, "admin permission required")
			c.Abort()
			return
		}
		c.Next()
	}
}
