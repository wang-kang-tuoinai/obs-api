package tracestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
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

// TraceQuery 是 FindTraces 的查询参数。
// Start/End 单位为毫秒（ms），内部会转换成 Jaeger 所需的微秒（µs）。
type TraceQuery struct {
	Service   string // 必填，Jaeger 强制要求
	Operation string // 可选，过滤具体操作名
	Start     int64  // 可选，时间范围起点（ms）
	End       int64  // 可选，时间范围终点（ms）
	Limit     int    // 可选，最多返回条数，合法范围 1-500，默认 200
}

// TODO每个span里的error记录exception.type:exception.message,现在只记录了exception.message
// TODOmysql中间件里记录的"db.collection.name": "users","db.operation.name": "","db.query.summary": " users",可以删掉前两个
// TODOGetTrace现在有两层作用，供AI分析单个Trace的工具，作为其他工具的基础设施，所以对GetTrace返回给agent的工具需要过滤一下
func (p *JaegerProvider) GetTrace(ctx context.Context, traceID string) (*Trace, error) {
	rawURL := fmt.Sprintf("%s/api/traces/%s", p.baseURL, traceID)
	traces, _, err := p.fetchAndBuildTraces(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	if len(traces) == 0 {
		return nil, nil // trace 不存在
	}
	return traces[0], nil
}

// FindTraces 按条件从 Jaeger 拉取一批 Trace。
// 返回值：成功解析的 Trace 列表、跳过原因的 notices、错误。
func (p *JaegerProvider) FindTraces(ctx context.Context, q TraceQuery) ([]*Trace, []string, error) {
	rawURL := p.buildQueryURL(q)
	return p.fetchAndBuildTraces(ctx, rawURL)
}

// buildQueryURL 将 TraceQuery 转成 Jaeger /api/traces 的查询 URL。
// Start/End 单位为 ms，转换成 Jaeger 所需的 µs。
func (p *JaegerProvider) buildQueryURL(q TraceQuery) string {
	params := url.Values{}
	params.Set("service", q.Service)
	if q.Operation != "" {
		params.Set("operation", q.Operation)
	}
	if q.Start > 0 {
		params.Set("start", strconv.FormatInt(q.Start*1000, 10)) // ms → µs
	}
	if q.End > 0 {
		params.Set("end", strconv.FormatInt(q.End*1000, 10)) // ms → µs
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	params.Set("limit", strconv.Itoa(limit))
	return fmt.Sprintf("%s/api/traces?%s", p.baseURL, params.Encode())
}

// fetchAndBuildTraces 是共用的底层方法：发 HTTP 请求 → 解析 jaegerResponse → 逐条 BuildTrace。
// 适用于 GetTrace（/api/traces/{id}）和 FindTraces（/api/traces?...）两种 URL 形式。
// 返回值：成功解析的 Trace 列表、跳过原因的 notices、错误。
func (p *JaegerProvider) fetchAndBuildTraces(ctx context.Context, rawURL string) ([]*Trace, []string, error) {
	// 1. 发 HTTP 请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 Jaeger 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, nil // trace 不存在
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("Jaeger 返回非 200 状态码: %d", resp.StatusCode)
	}

	// 2. 解析 jaegerResponse
	var jr jaegerResponse
	if err := json.NewDecoder(resp.Body).Decode(&jr); err != nil {
		return nil, nil, fmt.Errorf("解析 Jaeger 响应失败: %w", err)
	}
	if len(jr.Data) == 0 {
		return nil, nil, nil
	}

	// 3. 逐条归一化 Span + BuildTrace
	traces := make([]*Trace, 0, len(jr.Data))
	var skippedNonEntry, skippedBroken int
	for _, jt := range jr.Data {
		spans := make([]*Span, 0, len(jt.Spans))
		for _, js := range jt.Spans {
			spans = append(spans, toSpan(js, jt.Processes))
		}
		t, err := BuildTrace(jt.TraceID, spans)
		switch {
		case errors.Is(err, ErrNotEntrypoint):
			skippedNonEntry++
		case err != nil:
			skippedBroken++
			log.Printf("build trace %s failed: %v", jt.TraceID, err)
		default:
			traces = append(traces, t)
		}
	}

	var notices []string
	if skippedNonEntry > 0 {
		notices = append(notices, fmt.Sprintf(
			"跳过 %d 条非 HTTP 入口的 trace（如连接池拨号）", skippedNonEntry))
	}
	if skippedBroken > 0 {
		notices = append(notices, fmt.Sprintf(
			"有 %d 条 trace 结构异常无法解析，可能是数据不完整", skippedBroken))
	}
	return traces, notices, nil
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
