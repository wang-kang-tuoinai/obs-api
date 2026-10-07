package handler

import (
	"net/http"
	"obs-api/internal/logstore"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *VisualHandler) Logs(c *gin.Context) {
	if h.logs == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "日志面板未配置"})
		return
	}
	q := logstore.HistogramQuery{Service: strings.TrimSpace(c.Query("service")), Method: strings.TrimSpace(c.Query("method")), Route: strings.TrimSpace(c.Query("route"))}
	validMethod := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true}
	if q.Service == "" || len(q.Service) > 64 || len(q.Route) > 255 || (q.Method == "") != (q.Route == "") || (q.Method != "" && (!validMethod[q.Method] || !strings.HasPrefix(q.Route, "/"))) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 必填（最多 64 字节）；method 与 route 须成对提供，为标准大写 HTTP 方法和 / 开头路由（最多 255 字节）"})
		return
	}
	for key := range c.Request.URL.Query() {
		switch key {
		case "service", "method", "route", "start_ms", "end_ms":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的日志图表参数"})
			return
		}
	}
	if c.Request.URL.Query().Has("start_ms") || c.Request.URL.Query().Has("end_ms") {
		var e1, e2 error
		q.StartMs, e1 = strconv.ParseInt(c.Query("start_ms"), 10, 64)
		q.EndMs, e2 = strconv.ParseInt(c.Query("end_ms"), 10, 64)
		if e1 != nil || e2 != nil || q.StartMs <= 0 || q.EndMs <= q.StartMs || q.EndMs-q.StartMs > logstore.VisualWindowMs || q.EndMs > time.Now().Add(5*time.Second).UnixMilli() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start_ms/end_ms 须同时提供，窗口大于 0 且不超过 15 分钟，不能查询未来"})
			return
		}
	}
	result, err := h.logs.Snapshot(q)
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
