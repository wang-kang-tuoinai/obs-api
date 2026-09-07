package handler

import (
	"net/http"
	"obs-api/internal/tracestore"
	"strconv"

	"github.com/gin-gonic/gin"
)

type TraceHandler struct {
	provider tracestore.TraceProvider
}

func NewTraceHandler(provider tracestore.TraceProvider) *TraceHandler {
	return &TraceHandler{provider: provider}
}

// GetTrace 查询指定 trace_id 的链路数据并返回。
// GET /api/v1/traces/:trace_id
func (h *TraceHandler) GetTrace(c *gin.Context) {
	traceID := c.Param("trace_id")
	if traceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trace_id 不能为空"})
		return
	}

	trace, err := h.provider.GetTrace(c.Request.Context(), traceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if trace == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "trace 不存在"})
		return
	}

	c.JSON(http.StatusOK, trace)
}

// Stats 按条件批量拉取链路并聚合统计。
// GET /api/v1/traces/stats?service=X&operation=Y&start=s&end=e&limit=N
//
// 查询参数：
//   - service   必填，服务名
//   - operation 可选，操作名过滤，只能传 root span 的 operation
//   - start/end 秒级时间戳（同 /logs/stats），默认最近 1 小时，窗口封顶 7 天
//   - limit     采样条数上限，合法范围 1-500，默认 200
func (h *TraceHandler) Stats(c *gin.Context) {
	service := c.Query("service")
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 不能为空"})
		return
	}

	start, end, notices := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 500 {
		if limit != 0 {
			notices = append(notices, "limit 超出范围（1-500），已设为 200")
		}
		limit = 200
	}

	traces, fetchNotices, err := h.provider.FindTraces(c.Request.Context(), tracestore.TraceQuery{
		Service:   service,
		Operation: c.Query("operation"),
		Start:     start * 1000, // s → ms，TraceQuery.Start 单位为 ms
		End:       end * 1000,
		Limit:     limit,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	notices = append(notices, fetchNotices...)

	result, resultNotices := tracestore.Aggregate(traces)
	notices = append(notices, resultNotices...)

	c.JSON(http.StatusOK, gin.H{
		"stats":   result,
		"notices": notices,
	})
}
