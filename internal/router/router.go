package router

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

func SetupRouter(db *sql.DB) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		if err := db.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// TODO: 注册业务路由
	api := r.Group("/api/v1")
	{
		_ = api // 占位，后续在此注册路由
	}

	return r
}