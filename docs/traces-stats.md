# Trace 根入口统计

`GET /api/v1/traces/stats`；Agent 工具 `query_trace_stats`。

从用户请求视角统计指定服务的全局 `server` 根入口。Jaeger 按任意 Span 召回候选后，本地再次校验根入口；包含下游服务不等于以该服务为根入口。无唯一根、父节点缺失的片段不冒充根入口。

## 参数

| 参数 | 含义 |
| --- | --- |
| service | 必填，根入口所属服务 |
| operation | 可选，根入口操作名，精确匹配；留空统计所有入口接口 |
| start/end | 秒级 Unix 时间戳，默认最近一小时，窗口最长七天 |
| limit | Jaeger 候选 Trace 上限，默认 200，范围 1–500；非法值恢复默认并提示 |

Agent 可省略 service 使用 `TRACE_ENTRY_SERVICE`，默认 `ops-agent-backend`；HTTP API 仍要求 service。多个对外入口应分别查询，不能把下游调用相加当作用户请求数。

## 响应

顶层返回 `service`、`stats` 和 `notices`。

`stats.total_traces` 是最终匹配的根入口请求数；`by_status` 是相同样本的状态分布。`entrypoints` 按 `(service, operation)` 聚合，包含 `service`、`operation`、`count`、`p50_ms`、`p95_ms`、`p99_ms`、`failed`、`degraded`、`downstream_error_services`。

```json
{
  "service": "gateway",
  "operation": "GET /users/:id",
  "count": 100,
  "p50_ms": 50,
  "p95_ms": 350,
  "p99_ms": 500,
  "failed": 8,
  "degraded": 12,
  "downstream_error_services": [
    {"service": "profile-service", "request_count": 15},
    {"service": "user-service", "request_count": 8}
  ]
}
```

下游摘要遍历入口全部后代的有效错误，按错误 Span 自己的 service 归属，排除入口服务自身和缺失服务名的节点。同一入口请求内同一服务只计一次。多个服务计数不可相加，不是该下游服务自己的失败调用次数、错误率或根因结论。没有错误项时返回 `[]`。按计数降序、服务名升序排列。

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

只代表本次召回的入口请求样本，候选上限、小样本和缺失数据会写入 notices，不能宣称全窗口完整统计。耗时取根入口自身总耗时，包含等待下游，不累加 Span 耗时。

可将 entrypoints 中的 service/operation 原样传给 search；也可根据下游服务名搜索它自己的 server 入口，operation 留空或使用该下游的接口名。后者可能包括来自其他上游的请求，用 trace_id 核对关联。

缺少根入口、错误服务名等数据时保留提示，不按操作名或 IP 猜测服务归属。
