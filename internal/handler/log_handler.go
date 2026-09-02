package handler

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	lg "obs-api/internal/logstore"
)

type LogHandler struct {
	store lg.LogStore
}

func NewLogHandler(store lg.LogStore) *LogHandler {
	return &LogHandler{store: store}
}

func (h *LogHandler) Stats(c *gin.Context) {
	start, end := parseTimeRange(c)

	topN, _ := strconv.Atoi(c.Query("top_n"))
	if topN <= 0 || topN > 50 {
		topN = 10
	}

	result, err := h.store.QueryStats(c.Request.Context(), lg.StatsQuery{
		Service: c.Query("service"),
		Route:   c.Query("route"),
		Level:   c.Query("level"),
		Start:   start,
		End:     end,
		TopN:    topN,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 从 StatsResult 组装 StatsSummary
	var total, errorCount int64
	for _, cnt := range result.ByLevel {
		total += cnt
	}
	if ec, ok := result.ByLevel[lg.LevelError]; ok {
		errorCount = ec
	}
	var errorRate float64
	if total > 0 {
		errorRate = math.Round(float64(errorCount)/float64(total)*10000) / 10000
	}

	resp := lg.LogStatsResponse{
		Summary: lg.StatsSummary{
			Window:       lg.Window{Start: start, End: end},
			Total:        total,
			ErrorCount:   errorCount,
			ErrorRate:    errorRate,
			ByLevel:      result.ByLevel,
			TopTemplates: result.TopTemplates,
		},
		GeneratedAt: time.Now().Unix(),
	}
	c.JSON(http.StatusOK, resp)
}

func (h *LogHandler) Templates(c *gin.Context) {
	start, end := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	items, err := h.store.QueryTemplates(c.Request.Context(), lg.TemplatesQuery{
		Service: c.Query("service"),
		Level:   c.Query("level"),
		Route:   c.Query("route"),
		Start:   start,
		End:     end,
		Limit:   limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, lg.TemplatesResponse{Items: items})
}

// parseTimeRange 解析 start/end 秒级时间窗：默认最近 1 小时，窗口封顶 7 天。
func parseTimeRange(c *gin.Context) (start, end int64) {
	now := time.Now().Unix()
	start, _ = strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ = strconv.ParseInt(c.Query("end"), 10, 64)
	if end <= 0 {
		end = now
	}
	if start <= 0 {
		start = end - 3600 // 默认最近 1 小时
	}
	if end-start > 7*24*3600 { // 窗口封顶 7 天
		start = end - 7*24*3600
	}
	return start, end
}

