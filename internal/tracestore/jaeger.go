package tracestore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type JaegerProvider struct {
	baseURL string
	client  *http.Client
}

func NewJaegerProvider(baseURL string) *JaegerProvider {
	return &JaegerProvider{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second, // 整个请求（包含连接、发送、接收）的最大时间
		},
	}
}

// TODO每个span里的error记录exception.type:exception.message,现在只记录了exception.message
// TODOmysql中间件里记录的"db.collection.name": "users","db.operation.name": "","db.query.summary": " users",可以删掉前两个
// TODOGetTrace现在有两层作用，供AI分析单个Trace的工具，作为其他工具的基础设施，所以对GetTrace返回给agent的工具需要过滤一下
func (p *JaegerProvider) GetTrace(ctx context.Context, traceID string) (*Trace, error) {
	// 1. 发 HTTP 请求到 Jaeger
	url := fmt.Sprintf("%s/api/traces/%s", p.baseURL, traceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Jaeger 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // trace 不存在
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Jaeger 返回非 200 状态码: %d", resp.StatusCode)
	}

	// 2. 解析成 jaegerResponse
	var jr jaegerResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, fmt.Errorf("解析 Jaeger 响应失败: %w", err)
	}
	if len(jr.Data) == 0 {
		return nil, nil // trace 不存在
	}
	jt := jr.Data[0]

	// 3. 归一化成 []*Span
	spans := make([]*Span, 0, len(jt.Spans))
	for _, js := range jt.Spans {
		spans = append(spans, toSpan(js, jt.Processes))
	}

	// 4-5. 建树 + 算 self_ms + 判断 status + 组装 Trace
	return BuildTrace(jt.TraceID, spans)
}

var dropTags = map[string]bool{
	"otel.scope.name": true, "otel.scope.version": true,
	"network.peer.address": true, "network.peer.port": true,
	"network.protocol.version": true,
	"client.address":           true, "server.port": true,
	"user_agent.original": true, "url.scheme": true,
	"code.filepath": true, "code.lineno": true,
	"error":                   true, // 和 otel.status_code 重复
	"http.response.body.size": true,
}

// tags数组到扁平map
func normalizeTags(tags []jaegerTag) (attrs map[string]any, kind, status, statusDesc string) {
	attrs = map[string]any{}
	status = "ok"
	for _, t := range tags {
		switch t.Key {
		case "span.kind":
			kind, _ = t.Value.(string)
		case "otel.status_code":
			if s, _ := t.Value.(string); s == "ERROR" {
				status = "error"
			}
		case "otel.status_description":
			statusDesc, _ = t.Value.(string)
		default:
			if !dropTags[t.Key] {
				attrs[t.Key] = t.Value
			}
		}
	}
	return
}

// extractError 从 span 的 log 事件中提取异常信息。
// 格式："ExceptionType: exception message"，无 type 时退化为纯 message。
func extractError(logs []jaegerLog) string {
	for _, l := range logs {
		var isException bool
		var excType, excMsg string
		for _, f := range l.Fields {
			switch f.Key {
			case "event":
				if v, _ := f.Value.(string); v == "exception" {
					isException = true
				}
			case "exception.type":
				excType, _ = f.Value.(string)
			case "exception.message":
				excMsg, _ = f.Value.(string)
			}
		}
		if isException && excMsg != "" {
			if excType != "" {
				return excType + ": " + excMsg
			}
			return excMsg
		}
	}
	return ""
}

// 单个span的转换
func toSpan(js jaegerSpan, processes map[string]jaegerProcess) *Span {
	attrs, kind, status, desc := normalizeTags(js.Tags)
	parent := ""
	for _, r := range js.References {
		if r.RefType == "CHILD_OF" {
			parent = r.SpanID
			break
		}
	}
	return &Span{
		SpanID:       js.SpanID,
		ParentSpanID: parent,
		Service:      processes[js.ProcessID].ServiceName,
		Operation:    js.OperationName,
		Kind:         kind,
		StartMs:      js.StartTime / 1000,
		DurationMs:   float64(js.Duration) / 1000,
		Status:       status,
		StatusDesc:   desc,
		Error:        extractError(js.Logs),
		Attrs:        attrs,
	}
}
