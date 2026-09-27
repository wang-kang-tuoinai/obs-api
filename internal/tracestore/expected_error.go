package tracestore

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var mysqlDuplicate = regexp.MustCompile(`(?i)(?:^|:\s*)(?:Error )?1062(?: \([A-Z0-9]+\))?: Duplicate entry `)

// 仅识别已处理的业务冲突：mysql.Create/Update 上的 user.duplicate 标记，
// 以及同一服务、该调用内 MySQL Span 的明确重复键错误。绝不屏蔽整棵子树。
func markExpectedDuplicates(s *Span, handledService string) {
	if s.Kind == "server" || s.Service != handledService {
		handledService = ""
	}
	if marked, _ := s.Attrs["user.duplicate"].(bool); marked && s.Service != "" &&
		(s.Operation == "mysql.Create" || s.Operation == "mysql.Update") {
		handledService = s.Service
	}
	system := firstString(s.Attrs, "db.system.name", "db.system")
	if handledService != "" && s.Service == handledService && system == "mysql" && httpStatus(s) < 500 {
		messages := 0
		matched := true
		for _, message := range []string{s.Error, s.StatusDesc} {
			if message == "" {
				continue
			}
			messages++
			message = strings.TrimSpace(message)
			if message != "duplicated key not allowed" && !strings.HasSuffix(message, ": duplicated key not allowed") && !mysqlDuplicate.MatchString(message) {
				matched = false
			}
		}
		if matched && messages > 0 {
			s.ExpectedError = "handled_mysql_duplicate_key"
		}
	}
	for _, child := range s.Children {
		markExpectedDuplicates(child, handledService)
	}
}

func firstString(attrs map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := attrs[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func httpStatus(s *Span) int {
	for _, key := range []string{"http.response.status_code", "http.status_code"} {
		switch value := s.Attrs[key].(type) {
		case float64:
			return int(value)
		case int:
			return value
		case int64:
			return int(value)
		case json.Number:
			n, _ := value.Int64()
			return int(n)
		case string:
			n, _ := strconv.Atoi(value)
			return n
		}
	}
	return 0
}
