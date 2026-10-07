package handler

import (
	"net/http"
	"strconv"
	"strings"

	lg "obs-api/internal/logstore"

	"github.com/gin-gonic/gin"
)

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
