# 指定服务的日志模板查询

`GET /api/v1/logs/templates`

## 请求

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `service` | 是 | 目标服务；去除首尾空白后不能为空，否则 HTTP 400 |
| `start` / `end` | 否 | Unix 秒；默认最近 1 小时，上限 7 天，沿用公共时间参数修正规则 |
| `level` | 否 | 日志级别过滤，例如 WARN 或 ERROR |
| `route` / `method` | 否 | 路由、HTTP 方法精确过滤 |
| `limit` | 否 | 模板分组数上限，默认 200，范围 1–500；非法值回退 200 并提示 |

不知道服务时先查询 `/logs/stats`；已知服务时可直接查询。`/logs/search` 保持 service 可选，支持跨服务查询同一 trace_id 的原始日志。

## 响应

```json
{
  "service": "user-service",
  "items": [
    {
      "template": "failed to read cache for user",
      "level": "WARN",
      "count": 52,
      "first_seen": 1787000100,
      "last_seen": 1787003500,
      "sample": {
        "ts": 1787003500,
        "trace_id": "example-trace-id",
        "route": "/api/v1/users/:id",
        "method": "GET",
        "attrs": {"component": "redis", "err": "i/o timeout"}
      }
    }
  ],
  "has_more": true,
  "notices": ["匹配模板组数超过 limit，仅返回部分模板；可增大 limit（最多 500）或缩小时间窗口、level、route、method 范围。当前不支持模板游标分页"]
}
```

- 在单个服务范围内按 `(template, level)` 分组，按 count 降序，再按 template、level 升序排列；同一模板不同级别属于不同组。
- SQL 使用 `LIMIT ?`，绑定值为 `limit+1`；只有实际发现额外分组时 `has_more=true`，返回 items 仍最多 limit 项。恰好 limit 组不算截断。
- `count`、`first_seen`、`last_seen` 按完整匹配时间窗计算，不受返回模板组数上限影响。返回时间均为秒。
- `sample` 取该组最新一条日志，按 ts、id 倒序选取；不能由这一条样例推断同组所有日志的根因或路由相同。
- `has_more` 指还有模板分组，不是原始日志分页；没有 `next_cursor`。可提高 limit 或缩小查询范围，不能保证一定能用一次查询取全。
- 无匹配日志返回 `items: []`、`has_more: false`；数据库错误返回 HTTP 500。

运行 `go test ./...` 覆盖服务必填、limit 校验、空结果、恰好上限及超出上限。工具的参数与返回透传测试位于 `ops-diagnosis-agent/test_log_tools.py`。
