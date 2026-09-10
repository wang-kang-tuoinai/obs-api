package handler

import (
	"net/http"
	"obs-api/internal/tracestore"

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
