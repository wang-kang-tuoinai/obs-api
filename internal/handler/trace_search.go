package handler

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"obs-api/internal/tracestore"

	"github.com/gin-gonic/gin"
)

// Search returns bounded request summaries, never the full span tree.
func (h *TraceHandler) Search(c *gin.Context) {
	service := strings.TrimSpace(c.Query("service"))
	status := c.Query("status")
	order := c.DefaultQuery("sort", "duration_desc")
	if service == "" || (status != "" && status != "ok" && status != "degraded" && status != "failed") || (order != "duration_desc" && order != "start_desc") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 必填；status 仅支持 ok/degraded/failed；sort 仅支持 duration_desc/start_desc"})
		return
	}
	minimum := 0.0
	if raw, exists := c.GetQuery("min_duration_ms"); exists {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "min_duration_ms 必须是有限的非负数"})
			return
		}
		minimum = value
	}
	start, end, notices := parseTimeRange(c)
	readLimit := func(name string, fallback, maximum int) int {
		raw, exists := c.GetQuery(name)
		if !exists {
			return fallback
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maximum {
			notices = append(notices, name+" 非法，已恢复默认值 "+strconv.Itoa(fallback))
			return fallback
		}
		return n
	}
	limit := readLimit("limit", 10, 50)
	fetchLimit := readLimit("fetch_limit", 200, 500)
	operation := c.Query("operation")
	// TODO 如果jaeger支持minDurations，是否可以直接把minDurations拼进查询字符串
	traces, fetchedNotices, err := h.provider.FindTraces(c.Request.Context(), tracestore.TraceQuery{
		Service: service, Operation: operation, Start: start * 1000, End: end * 1000, Limit: fetchLimit,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	notices = append(notices, fetchedNotices...)
	notices = append(notices, "仅在本次 Jaeger 返回且成功解析的候选中筛选和排序；空结果不代表整个时间窗口无异常，排序不保证全窗口最慢")
	result := tracestore.Search(traces, tracestore.SearchOptions{
		Service: service, Operation: operation, StartMs: start * 1000, EndMs: end * 1000,
		Status: status, MinDurationMs: minimum, Sort: order, Limit: limit,
	})
	// TODO 这里可以定义一个返回模型
	c.JSON(http.StatusOK, gin.H{"items": result.Items, "meta": gin.H{
		"window": gin.H{"start": start, "end": end}, "fetch_limit": fetchLimit, "fetched_count": len(traces), "matched_count": result.MatchedCount, "returned_count": len(result.Items), "has_more_matches": result.HasMoreMatches,
	}, "notices": notices})
}
