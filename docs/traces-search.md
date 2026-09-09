# Trace 请求摘要检索

`GET /api/v1/traces/search`

用于在 stats 定位入口后，查找具体慢请求、失败请求或内部异常请求。每项代表一次请求，不返回完整 Span 树。

## 字段口径

- `operation`：根 Span 的入口 operation，精确匹配，例如 `GET /api/v1/users`；动态路由使用实际埋点名称（例如 `GET /api/v1/users/:id`），建议复制 stats.entrypoints 的值。不传表示所有根入口，不支持查询 Redis/MySQL 子操作。
- `service`：根 Span 所属服务。当前实现只保留根节点为 server 的 Trace，不会把下游服务的子 Span 重新当成根入口。
- `duration_ms`：根 Span 的总耗时，包含其内部调用等待，不是各 Span 耗时之和，也不是浏览器端耗时。
- `status`：整条已解析链路的分类，并非根 Span 的原始状态。沿用 stats 当前实现：根 HTTP 4xx 优先归为 ok；其余情况下根 Span 标记 error 或有异常消息为 failed；根未出错但后代出错为 degraded；否则 ok。慢请求可以是 ok，degraded 不一定变慢，也不证明执行了业务降级逻辑。

## 请求参数

| 参数 | 必填 | 默认值与约束 |
|---|---|---|
| service | 是 | 根入口服务，例如 ops-agent-backend；空值返回 400 |
| operation | 否 | 根入口名称，精确匹配 |
| start / end | 否 | 秒级 Unix 时间戳；复用 stats 时间规则，默认最近 1 小时，最大 7 天；调整写入 notices |
| status | 否 | ok / degraded / failed；不传表示全部，其他值返回 400 |
| min_duration_ms | 否 | 根耗时下限，包含等号；默认 0；支持小数；负数、NaN、无穷或无法解析返回 400 |
| sort | 否 | duration_desc（默认）或 start_desc；其他值返回 400 |
| limit | 否 | 最终摘要数量，默认 10，范围 1–50；非法值恢复默认并提示 |
| fetch_limit | 否 | Jaeger 候选获取上限，默认 200，范围 1–500；非法值恢复默认并提示 |

duration_desc 按根耗时降序，同耗时按开始时间降序，仍相同按 trace_id 升序。start_desc 按开始时间降序，同时间按 trace_id 升序。

## 示例

```http
GET /api/v1/traces/search?service=ops-agent-backend&operation=GET%20%2Fapi%2Fv1%2Fusers&status=degraded&min_duration_ms=100&sort=duration_desc&limit=10
```

以下为示意数据：

```json
{
  "items": [
    {
      "trace_id": "a1b2c3",
      "service": "ops-agent-backend",
      "operation": "GET /api/v1/users",
      "start_ms": 1787003500000,
      "duration_ms": 320.5,
      "status": "degraded",
      "error_summary": {"operation": "redis GET", "message": "connection refused"}
    }
  ],
  "meta": {
    "window": {"start": 1787000000, "end": 1787003600},
    "fetch_limit": 200,
    "fetched_count": 80,
    "matched_count": 1,
    "returned_count": 1,
    "has_more_matches": false
  },
  "notices": ["仅在本次 Jaeger 返回且成功解析的候选中筛选和排序；空结果不代表整个时间窗口无异常，排序不保证全窗口最慢"]
}
```

## 返回字段

| 字段 | 含义 |
|---|---|
| items[].trace_id | 请求链路 ID，可用于现有 `/traces/:trace_id` 或 `/logs/search?trace_id=...` 下钻 |
| items[].service / operation | 根 Span 所属服务与入口 |
| items[].start_ms | 根 Span 开始时间，毫秒级 Unix 时间戳；注意请求 start/end 使用秒 |
| items[].duration_ms | 根 Span 耗时，毫秒 |
| items[].status | 上述链路分类 |
| items[].error_summary | 可选，现有分析器选出的一条代表性错误；operation/message 各截断至 240 个 Unicode 字符并追加省略号；不是已验证根因，也不是全部异常 |
| items[].warnings | 可选，链路结构不完整等警告；状态仅基于解析成功的树 |
| meta.window | 实际采用的查询时间范围，秒 |
| meta.fetched_count | Jaeger 返回后成功解析的候选数，已排除非入口/无法解析链路；不是全窗口总请求数 |
| meta.matched_count | 候选内通过根入口、时间、状态和耗时筛选的条数，尚未应用 limit |
| meta.returned_count | 实际返回摘要数 |
| meta.has_more_matches | 本次候选中是否还有匹配结果因 limit 被省略；不是 Jaeger 分页标志 |
| notices | 时间或数量参数调整、候选截断、跳过数据及结果范围提示 |

## 查询边界和错误

先按服务、operation 和时间从 Jaeger 获取最多 fetch_limit 条候选，再校验根入口并在本地按状态和耗时筛选、排序，最后取 limit 条。两个 limit 独立；fetch_limit 小于 limit 时不会自动扩大候选范围。

该接口不提供游标分页，不保证全窗口扫描，也不承诺全窗口最慢 Top N。达到候选上限时应缩小时间窗口分段查询；提高 limit 只会增加输出，不会增加候选。即使 items 为空，也可能是候选内没有匹配项，不能据此断言系统无故障。原始候选截断提示沿用 provider 的 notices。

- 200：查询完成，无匹配时 items 为 []。
- 400：必填参数缺失或 status/sort/min_duration_ms 非法，返回 `{"error":"说明"}`。
- 502：Jaeger 请求或解析失败，返回 `{"error":"说明"}`。

Agent 工具名：`search_traces`，已注册在诊断 Agent 的工具列表中。宽泛问题先查 stats；已知接口时可直接 search；已知 trace_id 时直接查询详情或关联日志。
