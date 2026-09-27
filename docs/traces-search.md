# Trace 服务入口调用检索

`GET /api/v1/traces/search`；Agent 工具 `search_traces`。

每项代表指定服务的一次 `kind=server` 入口调用，以 `(trace_id, entry_span_id)` 标识，不要求是整条 Trace 的根。只返回摘要，不返回 Span 树。

## 请求参数

| 参数 | 含义与约束 |
| --- | --- |
| service | 必填，目标入口自己的服务名，精确匹配 |
| operation | 可选，目标服务自己的 HTTP/RPC 入口名；如 GET /api/v1/users/:id；不是上游接口、Redis GET 或 MySQL SELECT |
| start/end | 目标入口开始时间的范围，秒级 Unix 时间戳；默认最近一小时，最长七天 |
| status | 可选，目标入口及后代的 ok/degraded/failed 分类 |
| min_duration_ms | 目标入口耗时下限，含等号；默认 0，有限非负数，不能超过 Go time.Duration 范围 |
| sort | duration_desc（默认）或 start_desc |
| limit | 最终入口调用数，默认 10，范围 1–50 |
| fetch_limit | Jaeger 候选 Trace 上限，默认 200，范围 1–500 |

非法 service/status/sort/min_duration_ms 返回 400；非法 limit/fetch_limit 恢复默认并提示。时间调整规则与 stats 一致。

duration_desc 按入口耗时降序，再按开始时间降序；start_desc 按开始时间降序。其余相同时按 trace_id、entry_span_id 升序，保证稳定。重复的候选调用不会重复输出。

## 响应示例

```json
{
  "items": [{
    "trace_id": "a1b2c3",
    "entry_span_id": "user-entry-1",
    "service": "user-service",
    "operation": "GET /api/v1/users/:id",
    "start_ms": 1787003500000,
    "duration_ms": 350,
    "status": "degraded",
    "error_summary": {
      "service": "profile-service",
      "span_id": "profile-db-1",
      "operation": "SELECT profiles",
      "message": "i/o timeout"
    }
  }],
  "meta": {
    "window": {"start": 1787000000, "end": 1787003600},
    "fetch_limit": 200,
    "fetched_count": 80,
    "matched_count": 1,
    "returned_count": 1,
    "has_more_matches": false
  },
  "notices": []
}
```

以上 ID 和数值仅为示意。operation 不传时匹配该服务全部 server 入口。一个 Trace 两次调用该服务可返回两行。

## 分析与计数口径

- service、operation、start_ms、duration_ms 均来自选中的入口。耗时包含其执行期间等待下游的时间，不叠加下游耗时。
- status 和 error_summary 只分析目标入口及其后代，不受上游和兄弟分支错误影响。分类及重复键排除规则见 [stats 文档](traces-stats.md)。4xx 不再直接判 ok。
- error_summary 按子节点开始时间排序，以先后代后自身的深度优先顺序取第一个有效错误。所有字段来自同一个错误节点；不是最早错误、全部错误或已确认根因。operation/message 各最多保留 240 个 Unicode 字符后追加省略号。无有效错误时省略摘要。
- 错误节点的 service 是记录错误的服务，不是推测的故障依赖。例如应用记录的数据库调用错误，归属于该应用服务。
- fetched_count 是成功解析的候选 Trace 数；matched_count 是候选内符合条件的入口调用数，returned_count 是应用 limit 后返回的调用数。matched_count 可以大于 fetched_count。
- has_more_matches 只表示本批候选中有匹配调用被 limit 省略，不是 Jaeger 分页标志。
- warnings 提示链路不完整、缺失服务名等情况。上游缺失时仍可以检索已采集的下游入口，但不能把局部 ok 当作完整链路无异常。

## 候选查询与下钻

Jaeger 按 service、operation、时间和 minDuration 获取候选，随后在本地枚举目标服务的 server 入口，再按同一入口的时间、耗时和状态验证。min_duration_ms 大于 0 时转为例如 minDuration=100.5ms；0 时不传。

只对本批候选排序，不承诺全窗口最慢 Top N，也不提供游标分页。两个 limit 独立，增加最终 limit 不会增加候选召回范围；空结果不能证明整个窗口无异常。

调用 detail(trace_id) 后，用 entry_span_id 在 root 或 fragments 中定位入口。detail 的顶层耗时/状态仍是全局根视角，可以与本摘要不同。

由 stats 的下游服务名发起 search 时，不要将上游 operation 当作下游 operation。该服务查询可能包含其他上游的请求，仍需 trace_id 关联。缺少 server 埋点的服务可能无法搜索到入口，不能据此否定已有错误 Span。

- 200：查询完成；无匹配时 items 为 []。
- 400：参数非法，返回 error 说明。
- 502：Jaeger 请求或解析失败。
