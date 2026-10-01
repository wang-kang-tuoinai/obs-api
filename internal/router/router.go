package router

import (
	"database/sql"
	"net/http"
	"obs-api/internal/handler"

	"github.com/gin-gonic/gin"
)

func SetupRouter(db *sql.DB, lh *handler.LogHandler, th *handler.TraceHandler, visual ...*handler.VisualHandler) *gin.Engine {
	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		if err := db.PingContext(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	if len(visual) > 0 {
		api.GET("/visual/services", visual[0].Services)
		api.GET("/visual/traces", visual[0].Traces)
	}
	{
		api.GET("/logs/stats", lh.Stats)
		api.GET("/logs/templates", lh.Templates)
		api.GET("/logs/search", lh.Search)

		api.GET("/traces/stats", th.Stats)
		api.GET("/traces/search", th.Search)
		api.GET("/traces/:trace_id", th.GetTrace)
	}

	return r
}
