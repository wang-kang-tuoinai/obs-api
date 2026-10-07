package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

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
