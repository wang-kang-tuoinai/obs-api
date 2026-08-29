package handler

import (
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
	now := time.Now().Unix()

	start, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ := strconv.ParseInt(c.Query("end"), 10, 64)
	if end <= 0 {
		end = now
	}
	if start <= 0 {
		start = end - 3600 // 默认最近 1 小时
	}
	if end-start > 7*24*3600 { // 窗口封顶 7 天
		start = end - 7*24*3600
	}

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

	c.JSON(http.StatusOK, result)
}
