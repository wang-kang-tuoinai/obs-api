package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

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
