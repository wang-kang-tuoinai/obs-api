package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"obs-api/internal/tracestore"
	"strings"
)

// Stats 查询指定服务入口：未指定 operation 时按操作分批，指定时使用更高预算。
// GET /api/v1/traces/stats?service=X&operation=Y&start=s&end=e
func (h *TraceHandler) Stats(c *gin.Context) {
	service := strings.TrimSpace(c.Query("service"))
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 不能为空"})
		return
	}
	if _, exists := c.Request.URL.Query()["limit"]; exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "stats 不再支持 limit；候选预算由服务端按是否指定 operation 决定"})
		return
	}
	start, end, notices := parseTimeRange(c)
	result, err := tracestore.CollectStats(c.Request.Context(), h.provider, tracestore.TraceQuery{
		Service: service, Operation: strings.TrimSpace(c.Query("operation")), Start: start * 1000, End: end * 1000,
	}, h.statsOptions)
	if result == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	notices = append(notices, result.Notices...)
	notices = append(notices, "仅统计各 operation 已获取的 server 入口及其后代，不包含上游和旁支；total_calls 按 (trace_id, entry_span_id) 计数。下游 request_count 按入口调用去重，服务间不可相加，不代表根因。未触及候选上限也不保证采集完整。")
	code := http.StatusOK
	if err != nil {
		code = http.StatusBadGateway
	}
	c.JSON(code, TraceStatsResponse{
		Service: service, Stats: result.Stats,
		Meta:    TraceStatsMeta{Window: TraceWindow{Start: start, End: end}, PerOperationLimit: result.PerOperationLimit, FetchedTraces: result.FetchedTraces, OperationQueries: result.OperationQueries},
		Notices: notices,
	})
}
