package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"obs-api/internal/tracestore"
	"strconv"
	"strings"
)

// Stats 按条件批量拉取链路并聚合统计。
// GET /api/v1/traces/stats?service=X&operation=Y&start=s&end=e&limit=N
//
// 查询参数：
//   - service   必填，服务名
//   - operation 可选，指定服务 server 入口的 operation，不要求全局根
//   - start/end 秒级时间戳（同 /logs/stats），默认最近 1 小时，窗口封顶 7 天
//   - limit     采样条数上限，合法范围 1-500，默认 200
func (h *TraceHandler) Stats(c *gin.Context) {
	service := strings.TrimSpace(c.Query("service"))
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

	selected, selectionNotices := tracestore.SelectServiceEntries(traces, tracestore.SearchOptions{
		Service: service, Operation: c.Query("operation"), StartMs: start * 1000, EndMs: end * 1000,
	})
	notices = append(notices, selectionNotices...)
	result, resultNotices := tracestore.Aggregate(selected)
	notices = append(notices, resultNotices...)
	notices = append(notices, "仅统计已召回的指定服务 server 入口及其后代，不包含上游或旁支；total_calls 按 (trace_id, entry_span_id) 计数，同一 Trace 可有多次调用。下游 request_count 按入口调用去重，服务间不可相加，不代表根因或下游自身错误率。仅排除已确认的 MySQL 重复键业务冲突。")
	traceIDs := make(map[string]struct{})
	for _, trace := range traces {
		if trace != nil {
			traceIDs[trace.TraceID] = struct{}{}
		}
	}

	c.JSON(http.StatusOK, TraceStatsResponse{
		Service: service,
		Stats:   result,
		Meta:    TraceStatsMeta{Window: TraceWindow{Start: start, End: end}, FetchLimit: limit, FetchedTraces: len(traceIDs)},
		Notices: notices,
	})
}
