package router

import (
	"database/sql"
	"net/http"
	"obs-api/internal/handler"

	"github.com/gin-gonic/gin"
)

func SetupRouter(db *sql.DB, lh *handler.LogHandler, th *handler.TraceHandler) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		if err := db.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		api.GET("/logs/stats", lh.Stats)
		api.GET("/logs/templates", lh.Templates)
		api.GET("/logs/search", lh.Search)

		api.GET("/traces/stats", th.Stats)
		api.GET("/traces/:trace_id", th.GetTrace)
	}

	return r
}