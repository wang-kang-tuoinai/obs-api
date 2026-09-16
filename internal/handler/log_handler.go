package handler

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	lg "obs-api/internal/logstore"

	"github.com/gin-gonic/gin"
)

type LogHandler struct {
	store lg.LogStore
}

func NewLogHandler(store lg.LogStore) *LogHandler {
	return &LogHandler{store: store}
}

//TODO - [ ] logs/stats 的 error_count 包含双重计数（access log 5xx + 业务 ERROR），
//   Agent 目前靠自己推导才能得到真实失败请求数（观察到它两次都推对了，
//   但依赖"所有 5xx 都走 HandleError"这个可能失效的假设）。

//   候选方案：
//   1. 加 kind 列（access/business），响应里单独给 failed_requests
//   2. 消除双重计数（中间件不记 ERROR，或 HandleError 不记）
//   3. 维持现状，靠模型推导

// TODO 现在Stats如果不指定service查询出来的是所有服务的日志，对于目前单体服务没问题
// 但是如果是多服务，可能需要标明每个服务的错误数
func (h *LogHandler) Stats(c *gin.Context) {
	start, end, notices := parseTimeRange(c)

	topN, _ := strconv.Atoi(c.Query("top_n"))
	if topN <= 0 || topN > 50 {
		topN = 10
		notices = append(notices, "top_n 超出范围（1-50），已设为 10")
	}

	result, err := h.store.QueryStats(c.Request.Context(), lg.StatsQuery{
		Service: c.Query("service"),
		Route:   c.Query("route"),
		Method:  c.Query("method"),
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
		Notices:     notices,
	}
	c.JSON(http.StatusOK, resp)
}

//TODO Template接口返回的sample字段只取了最新的一条log，但是这条log可能会发生在多个路由，
// 容易让大模型误以为这个Template只发生在某个路由上，
// 后续可以考虑返回多条sample，或者返回sample的路由列表

//TODO- [ ] internal server error 模板过于笼统，同一模板下可能混着多种不同根因
//   （mysql 连接失败、cache 反序列化失败、bcrypt 失败等）。
//   当前 templates 接口的 sample 只取最新一条，可能掩盖占比更高的其他错误。

// 候选方案：
// 1. 按 error wrapping 前缀分成几个子模板（便宜，但字符串匹配脆弱）
// 2. 定义分层的哨兵错误类型，HandleError 按类型精确分类（正确，成本高）
// 3. templates 接口支持返回多条 sample 或错误分布（改接口，treat symptom）
func (h *LogHandler) Templates(c *gin.Context) {
	start, end, notices := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
		notices = append(notices, "limit 超出范围（1-500），已设为 200")
	}

	items, err := h.store.QueryTemplates(c.Request.Context(), lg.TemplatesQuery{
		Service: c.Query("service"),
		Level:   c.Query("level"),
		Route:   c.Query("route"),
		Method:  c.Query("method"),
		Start:   start,
		End:     end,
		Limit:   limit,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, lg.TemplatesResponse{Items: items, Notices: notices})
}

func (h *LogHandler) Search(c *gin.Context) {
	start, end, notices := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
		notices = append(notices, "limit 超出范围（1-100），已设为 50")
	}

	// 游标格式 "ts:id"，ts 为毫秒
	var cursorTs int64
	var cursorID uint64
	if cursor := c.Query("cursor"); cursor != "" {
		parts := strings.SplitN(cursor, ":", 2)
		if len(parts) != 2 {
			notices = append(notices, "cursor 格式非法，已忽略")
		} else {
			ts, err1 := strconv.ParseInt(parts[0], 10, 64)
			id, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				notices = append(notices, "cursor 数值非法，已忽略")
			} else {
				cursorTs, cursorID = ts, id
			}
		}
	}

	result, err := h.store.QuerySearch(c.Request.Context(), lg.SearchQuery{
		Service:  c.Query("service"),
		Level:    c.Query("level"),
		Route:    c.Query("route"),
		Method:   c.Query("method"),
		TraceID:  c.Query("trace_id"),
		Template: c.Query("template"),
		Keyword:  c.Query("keyword"),
		Start:    start,
		End:      end,
		Limit:    limit,
		CursorTs: cursorTs,
		CursorID: cursorID,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result.Notices = notices
	c.JSON(http.StatusOK, result)
}

// parseTimeRange 解析 start/end 秒级时间窗：默认最近 1 小时，窗口封顶 7 天。
// 对参数的默认/修正会记录到 notices 返回，供上层告知调用方。
func parseTimeRange(c *gin.Context) (start, end int64, notices []string) {
	now := time.Now().Unix()
	start, _ = strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ = strconv.ParseInt(c.Query("end"), 10, 64)
	if end <= 0 {
		end = now
		notices = append(notices, "end 未提供或非法，已默认为当前时间")
	}
	if start <= 0 {
		start = end - 3600 // 默认最近 1 小时
		notices = append(notices, "start 未提供或非法，已默认为 end 前 1 小时")
	}
	if end-start > 7*24*3600 { // 窗口封顶 7 天
		start = end - 7*24*3600
		notices = append(notices, "时间窗口超过 7 天，已截断为最近 7 天")
	}
	if start >= end {
		start = end - 3600
		notices = append(notices, "start 大于等于 end，已重置为 end 前 1 小时")
	}
	return start, end, notices
}
