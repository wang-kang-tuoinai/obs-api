package handler

import (
	"net/http"
	"strings"
	"time"

	lg "obs-api/internal/logstore"

	"github.com/gin-gonic/gin"
)

// Stats 统计日志条数；同一次请求可以记录多条 ERROR，不能换算为失败请求数。
func (h *LogHandler) Stats(c *gin.Context) {
	for _, key := range []string{"level", "top_n"} {
		if _, supplied := c.Request.URL.Query()[key]; supplied {
			c.JSON(http.StatusBadRequest, gin.H{"error": "logs/stats 不再支持 " + key + "；模板筛选请使用 logs/templates"})
			return
		}
	}
	start, end, notices := parseTimeRange(c)

	result, err := h.store.QueryStats(c.Request.Context(), lg.StatsQuery{
		Service: strings.TrimSpace(c.Query("service")),
		Route:   c.Query("route"),
		Method:  c.Query("method"),
		Start:   start,
		End:     end,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if result.Summaries == nil {
		result.Summaries = []lg.StatsSummary{}
	}
	notices = append(notices, result.Notices...)
	notices = append(notices, "error_count/error_rate 按各服务匹配日志计算，不是失败请求数或请求失败率；未出现的服务不代表正常")

	resp := lg.LogStatsResponse{
		Window:      lg.Window{Start: start, End: end},
		Summaries:   result.Summaries,
		GeneratedAt: time.Now().Unix(),
		Notices:     notices,
	}
	c.JSON(http.StatusOK, resp)
}
