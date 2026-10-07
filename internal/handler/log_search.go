package handler

import (
	"net/http"
	"strconv"
	"strings"

	lg "obs-api/internal/logstore"

	"github.com/gin-gonic/gin"
)

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
