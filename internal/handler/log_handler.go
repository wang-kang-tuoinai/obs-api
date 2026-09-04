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

func (h *LogHandler) Stats(c *gin.Context) {
	start, end := parseTimeRange(c)

	topN, _ := strconv.Atoi(c.Query("top_n"))
	if topN <= 0 || topN > 50 {
		topN = 10
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
	start, end := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
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
	c.JSON(http.StatusOK, lg.TemplatesResponse{Items: items})
}

func (h *LogHandler) Search(c *gin.Context) {
	start, end := parseTimeRange(c)

	limit, _ := strconv.Atoi(c.Query("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// 游标格式 "ts:id"，ts 为毫秒
	var cursorTs int64
	var cursorID uint64
	if cursor := c.Query("cursor"); cursor != "" {
		parts := strings.SplitN(cursor, ":", 2)
		if len(parts) == 2 {
			cursorTs, _ = strconv.ParseInt(parts[0], 10, 64)
			cursorID, _ = strconv.ParseUint(parts[1], 10, 64)
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
	c.JSON(http.StatusOK, result)
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
