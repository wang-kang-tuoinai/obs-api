package handler

import (
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
	service := strings.TrimSpace(c.Query("service"))
	if service == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 为必填参数；未知服务时可先查询 logs/stats"})
		return
	}
	start, end, notices := parseTimeRange(c)

	limit := 200
	if raw, supplied := c.GetQuery("limit"); supplied || c.Request.URL.Query().Has("limit") {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 500 {
			notices = append(notices, "limit 非法或超出范围（1-500），已设为 200")
		} else {
			limit = parsed
		}
	}

	result, err := h.store.QueryTemplates(c.Request.Context(), lg.TemplatesQuery{
		Service: service,
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
	if result.Items == nil {
		result.Items = []lg.TemplateStat{}
	}
	if result.HasMore {
		notices = append(notices, "匹配模板组数超过 limit，仅返回部分模板；可增大 limit（最多 500）或缩小时间窗口、level、route、method 范围。当前不支持模板游标分页")
	}
	c.JSON(http.StatusOK, lg.TemplatesResponse{Service: service, Items: result.Items, HasMore: result.HasMore, Notices: notices})
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
