# Trace 服务入口统计

`GET /api/v1/traces/stats`；Agent 工具 `query_trace_stats`。

统计指定服务的 `kind=server` 入口及其后代，不要求入口位于整条 Trace 的根部。与 search 共用基础入口选择逻辑，Jaeger 召回候选后，本地校验入口的 service、operation 和开始时间；不包含上游及旁支。多根、缺少上游的片段中可识别的 server 入口仍可统计，同时保留不完整提示。

## 参数

| 参数 | 含义 |
| --- | --- |
| service | 必填，目标服务名，精确匹配；一次查询一个服务 |
| operation | 可选，目标服务 server 入口操作名，精确匹配；留空统计所有匹配入口接口 |
| start/end | 秒级 Unix 时间戳，筛选目标入口开始时间，包含两端；默认最近一小时，窗口最长七天 |

stats 已移除对外 limit 参数，传入（包括空值）返回 400；search 的 limit/fetch_limit 不变。

## 按 operation 查询

- 未指定 operation：查询 Jaeger `/api/operations?service=...&spanKind=server`，去重后按名称排序，每个 operation 最多拉取 1500 条候选 Trace。
- 指定 operation：直接查询，不依赖 operation 目录；候选上限提高到 5000。
- 每批仅统计该 operation 对应的服务入口，按 `(trace_id, entry_span_id)` 去重后统一聚合。不能把同一 Trace 中其他 operation 的入口混进该批。
- operation 目录没有时间窗口筛选，历史操作在当前窗口没有数据是正常情况。空目录只表示未发现 server 操作，不证明业务正常。
- 最多 3 路并发，目录与分批查询共用 25 秒预算；单次最多查询 30 个 operation，超出部分标 skipped。未启动的超时任务标 skipped，已启动但失败的任务标 failed。无需递归拆分时间窗口。

预算是本项目配置，不是 Jaeger 固定最大值。obs-api 支持环境变量 `TRACE_STATS_PER_OPERATION_LIMIT`（默认 1500）、`TRACE_STATS_FOCUSED_LIMIT`（默认 5000），须满足 `1 <= 前者 < 后者 <= 5000`，非法配置启动失败。

概览触顶时指定该 operation 使用更高预算重查；单接口仍触顶则缩小时间窗口。1500/5000 限制候选 Trace 数，不限制入口调用次数。

Agent 可省略 service 使用 `TRACE_ENTRY_SERVICE`，默认 `ops-agent-backend`；HTTP API 仍要求 service。该配置表示默认查询的单个服务，不要求是全局根入口服务。多个服务应分别查询，不能把调用数相加当作用户请求数。本接口不增加 status、min_duration_ms、sort 筛选，按状态或耗时下钻请使用 search。

## 响应

顶层返回 `service`、`stats`、`meta` 和 `notices`。

`stats.total_calls` 是按 `(trace_id, entry_span_id)` 去重后的入口调用数；取代旧字段 `total_traces`。同一 Trace 多次进入目标服务，分别计数。嵌套的匹配入口也分别计数，子树可能重叠，不代表独立用户请求。

`by_status` 固定包含 ok/degraded/failed 三个计数，其和等于 total_calls。`entrypoints` 按 `(service, operation)` 聚合，分组 count 之和等于 total_calls，按 count 降序、service/operation 升序排列。

`meta.window` 返回实际查询的 start/end（秒）；`meta.per_operation_limit` 取代 fetch_limit，返回本次每个 operation 的实际候选上限。`meta.fetched_traces` 是所有成功查询中成功解析后不同 trace_id 的数量，跨 operation 去重；不是原始数量之和，也不是窗口全部 Trace 数。一个候选可包含多个入口，因此 total_calls 可以超过 fetched_traces。

`meta.operation_queries` 对每个发现或指定的 operation 返回：

| 字段 | 含义 |
| --- | --- |
| operation | 查询的操作名 |
| status | success / failed / skipped；success 表示查询成功，不保证候选无截断或采集完整 |
| raw_trace_count | 成功时为 Jaeger 原始返回数量，包括无法解析的 Trace；成功无数据为 0；failed/skipped 为 null |
| limit_reached | 仅 success 时判断原始候选是否达到上限；true 表示可能截断，false 不保证采集完整 |
| message | 可选的触顶建议、查询错误或跳过原因 |

单接口或所有操作查询失败/未执行时返回 502，并保留 operation_queries；发现目录失败返回 502 error。部分成功时返回 200，stats 只包含成功查询的有效入口，notices 明确标记不完整。结构异常而无法解析的记录会保留原始计数并在 notices 提示，不因解析后的数量变少就忽略触顶风险。

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
    "per_operation_limit": 1500,
    "fetched_traces": 2,
    "operation_queries": [{
      "operation": "GET /users/:id",
      "status": "success",
      "raw_trace_count": 2,
      "limit_reached": false
    }]
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

对同一 operation 的相同候选和基础筛选条件，其 count 等于 search 在状态、耗时过滤及返回数量截断之前的匹配数。stats 按 operation 分批且预算更大，实际 search 查询的候选集合可能不同；独立 HTTP 查询的数据也可能变化，不承诺始终相等。多次重查的统计不可直接相加，避免重复计算和混合百分位。
