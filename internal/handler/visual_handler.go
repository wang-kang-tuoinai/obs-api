package handler

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"obs-api/internal/tracestore"
	"strconv"
	"strings"
	"time"
)

type ServiceLister interface {
	GetServices(context.Context) ([]string, error)
}
type VisualHandler struct {
	cache          *tracestore.VisualCache
	services       ServiceLister
	defaultService string
}

func NewVisualHandler(cache *tracestore.VisualCache, services ServiceLister, defaultService string) *VisualHandler {
	return &VisualHandler{cache, services, defaultService}
}
func (h *VisualHandler) Services(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	services, err := h.services.GetServices(ctx)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "无法读取 Jaeger 服务目录", "default_service": h.defaultService})
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": services, "default_service": h.defaultService})
}
func (h *VisualHandler) Traces(c *gin.Context) {
	service := strings.TrimSpace(c.Query("service"))
	operation := c.Query("operation")
	if service == "" || len(service) > 128 || len(operation) > 512 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "service 必填（最多 128 字节），operation 最多 512 字节"})
		return
	}
	var start, end int64
	if c.Request.URL.Query().Has("start_ms") || c.Request.URL.Query().Has("end_ms") {
		var e1, e2 error
		start, e1 = strconv.ParseInt(c.Query("start_ms"), 10, 64)
		end, e2 = strconv.ParseInt(c.Query("end_ms"), 10, 64)
		if e1 != nil || e2 != nil || start <= 0 || end <= start || end-start > int64((15*time.Minute)/time.Millisecond) || end > time.Now().Add(5*time.Second).UnixMilli() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "历史 start_ms/end_ms 须同时提供，范围大于 0 且不超过 15 分钟，不能查询未来"})
			return
		}
	}
	result, err := h.cache.Snapshot(service, operation, start, end)
	if err != nil {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}
