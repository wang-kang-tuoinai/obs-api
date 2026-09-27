package handler

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		// Jaeger 使用 time.ParseDuration；提前拒绝超出其表示范围的值。
		if _, err := time.ParseDuration(strconv.FormatFloat(value, 'f', -1, 64) + "ms"); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "min_duration_ms 超出 Jaeger 支持的耗时范围"})
			return
		}
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
	traces, fetchedNotices, err := h.provider.FindTraces(c.Request.Context(), tracestore.TraceQuery{
		Service: service, Operation: operation, Start: start * 1000, End: end * 1000, Limit: fetchLimit,
		MinDurationMs: minimum,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	notices = append(notices, fetchedNotices...)
	notices = append(notices, "仅在本次 Jaeger 返回且成功解析的候选中筛选和排序；空结果不代表整个时间窗口无异常，排序不保证全窗口最慢")
	notices = append(notices, "每项为指定服务的一次 server 入口调用，以 trace_id + entry_span_id 标识；状态/耗时/错误摘要仅观察该入口及后代。fetched_count 是 Trace 数，matched_count/returned_count 是入口调用数。同一 Trace 可出现多项。")
	result := tracestore.Search(traces, tracestore.SearchOptions{
		Service: service, Operation: operation, StartMs: start * 1000, EndMs: end * 1000,
		Status: status, MinDurationMs: minimum, Sort: order, Limit: limit,
	})
	c.JSON(http.StatusOK, TraceSearchResponse{
		Items: result.Items,
		Meta: TraceSearchMeta{
			Window:     TraceWindow{Start: start, End: end},
			FetchLimit: fetchLimit, FetchedCount: len(traces),
			MatchedCount: result.MatchedCount, ReturnedCount: len(result.Items),
			HasMoreMatches: result.HasMoreMatches,
		},
		Notices: notices,
	})
}
