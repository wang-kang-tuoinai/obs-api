# Trace 服务入口统计

`GET /api/v1/traces/stats`；Agent 工具 `query_trace_stats`。

统计指定服务的 `kind=server` 入口及其后代，不要求入口位于整条 Trace 的根部。与 search 共用基础入口选择逻辑，Jaeger 召回候选后，本地校验入口的 service、operation 和开始时间；不包含上游及旁支。多根、缺少上游的片段中可识别的 server 入口仍可统计，同时保留不完整提示。

## 参数

| 参数 | 含义 |
| --- | --- |
| service | 必填，目标服务名，精确匹配；一次查询一个服务 |
| operation | 可选，目标服务 server 入口操作名，精确匹配；留空统计所有匹配入口接口 |
| start/end | 秒级 Unix 时间戳，筛选目标入口开始时间，包含两端；默认最近一小时，窗口最长七天 |
| limit | Jaeger 候选 Trace 上限，默认 200，范围 1–500；非法值恢复默认并提示 |

Agent 可省略 service 使用 `TRACE_ENTRY_SERVICE`，默认 `ops-agent-backend`；HTTP API 仍要求 service。该配置表示默认查询的单个服务，不要求是全局根入口服务。多个服务应分别查询，不能把调用数相加当作用户请求数。本接口不增加 status、min_duration_ms、sort 筛选，按状态或耗时下钻请使用 search。

## 响应

顶层返回 `service`、`stats`、`meta` 和 `notices`。

`stats.total_calls` 是按 `(trace_id, entry_span_id)` 去重后的入口调用数；取代旧字段 `total_traces`。同一 Trace 多次进入目标服务，分别计数。嵌套的匹配入口也分别计数，子树可能重叠，不代表独立用户请求。

`by_status` 固定包含 ok/degraded/failed 三个计数，其和等于 total_calls。`entrypoints` 按 `(service, operation)` 聚合，分组 count 之和等于 total_calls，按 count 降序、service/operation 升序排列。

`meta.window` 返回实际查询的 start/end（秒）；`meta.fetch_limit` 是实际候选上限；`meta.fetched_traces` 是 provider 成功解析后不同 trace_id 的数量，不是窗口全部 Trace 数。一个候选可包含多个入口，因此 total_calls 可以超过 fetched_traces，甚至超过 fetch_limit。

无匹配入口时返回 200，total_calls 和状态计数均为 0，entrypoints 为 `[]`。以下为完整响应示例：

```json
{
  "service": "user-service",
  "stats": {
    "total_calls": 3,
    "by_status": {"ok": 1, "degraded": 1, "failed": 1},
    "entrypoints": [{
      "service": "user-service",
      "operation": "GET /users/:id",
      "count": 3,
      "p50_ms": 100,
      "p95_ms": 300,
      "p99_ms": 300,
      "failed": 1,
      "degraded": 1,
      "downstream_error_services": [
        {"service": "profile-service", "request_count": 2}
      ]
    }]
  },
  "meta": {
    "window": {"start": 1789479485, "end": 1789479545},
    "fetch_limit": 200,
    "fetched_traces": 2
  },
  "notices": ["仅统计已召回的服务入口调用；样本较小，百分位仅供参考。"]
}
```

下游摘要遍历入口全部后代的有效错误，按错误 Span 自己的 service 归属，排除入口服务自身和缺失服务名的节点。同一次入口调用内同一服务只计一次。request_count 表示包含该下游服务错误证据的当前服务入口调用数；多个服务计数不可相加，不是该下游服务自己的失败调用次数、错误率或根因结论。没有错误项时返回 `[]`。按计数降序、服务名升序排列。缺失服务名的错误仍参与状态分类，但不猜测其归属。

## 三个工具共用的错误规则

- 入口自身有有效错误或 HTTP 5xx：failed。
- 入口未失败，但后代有有效错误：degraded。
- 已采集范围没有有效错误：ok，不等于业务成功或 HTTP 2xx。
- 4xx 不再提前返回 ok；4xx 同时伴有 Redis 超时等异常时仍会体现为 degraded/failed。
- 第一版只排除已确认的 MySQL 重复键业务冲突：同一服务 `mysql.Create`/`mysql.Update` 标有 `user.duplicate=true`，其对应 MySQL 子 Span 明确记录 `gorm.ErrDuplicatedKey` 的消息或 MySQL 1062 Duplicate entry。缺少业务标记、服务或数据库类型不匹配、其他错误，均不排除。
- 排除只影响分类、下游计数和代表性摘要，不删除原始错误；detail 中用 `expected_error=handled_mysql_duplicate_key` 解释排除。
- Span 自身标记 error、记录异常消息或返回 HTTP 5xx 都属于原始错误证据；不是自动的根因判断。

`degraded` 不保证执行过业务降级，也不保证请求变慢。只有慢、没有有效错误的请求可以为 ok，仍需通过耗时筛选和 detail 分析。

## 范围与下钻

只代表本次召回的入口调用样本，候选上限、小样本和缺失数据会写入 notices，不能宣称全窗口完整统计。耗时取选中入口自身总耗时，包含等待下游，不累加 Span 耗时。百分位沿用最近秩算法：排序后取 ceil(p × n) 对应的样本，保留三位小数。

可将 entrypoints 中的 service/operation 原样传给 search；也可根据下游服务名搜索它自己的 server 入口，operation 留空或使用该下游的接口名。后者可能包括来自其他上游的请求，用 trace_id 核对关联。

缺少全局根不妨碍已识别服务入口的统计。无法连接到该入口的片段不能强行归入其后代；缺失后代可能低估错误。stats 不修改原 Trace.Root，detail 仍展示全局链路，需用 search 返回的 entry_span_id 定位入口。

对同一批候选和相同基础筛选条件，total_calls 等于 search 在状态、耗时过滤及返回数量截断之前的匹配数；独立 HTTP 查询的数据可能变化，不承诺始终相等。
