package tracestore

// JaegerResponse 表示 Jaeger 查询返回的完整响应
type jaegerResponse struct {
	Data   []jaegerTrace `json:"data"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
	Errors interface{}   `json:"errors"`
}

// Trace 表示一个完整的追踪链路
type jaegerTrace struct {
	TraceID   string                   `json:"traceID"`
	Spans     []jaegerSpan             `json:"spans"`
	Processes map[string]jaegerProcess `json:"processes"`
	// 一般没有warnings不解析
	// Warnings  []string           `json:"warnings"`
}

// Span 表示一个操作片段
type jaegerSpan struct {
	TraceID       string            `json:"traceID"`
	SpanID        string            `json:"spanID"`
	OperationName string            `json:"operationName"`
	References    []jaegerReference `json:"references"`
	StartTime     int64             `json:"startTime"` // 微秒
	Duration      int64             `json:"duration"`  // 微秒
	Tags          []jaegerTag       `json:"tags"`
	Logs          []jaegerLog       `json:"logs"`
	ProcessID     string            `json:"processID"`
	// 一般没有warnings不解析
	// Warnings      []string    `json:"warnings"`
}

// Reference 表示 Span 之间的引用关系
type jaegerReference struct {
	RefType string `json:"refType"` // "CHILD_OF" 或 "FOLLOWS_FROM"
	TraceID string `json:"traceID"`
	SpanID  string `json:"spanID"`
}

// Tag 表示键值对标签，值类型由 Type 字段指定
type jaegerTag struct {
	Key   string      `json:"key"`
	Type  string      `json:"type"` // "string", "int64", "bool" 等
	Value interface{} `json:"value"`
}

// Log 表示 Span 中的日志事件
type jaegerLog struct {
	Timestamp int64       `json:"timestamp"` // 微秒
	Fields    []jaegerTag `json:"fields"`
}

// Process 表示产生 Span 的进程/服务信息
type jaegerProcess struct {
	ServiceName string      `json:"serviceName"`
	Tags        []jaegerTag `json:"tags"`
}
