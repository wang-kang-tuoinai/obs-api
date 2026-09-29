# 日志多服务统计

`GET /api/v1/logs/stats`

用于发现查询窗口内哪些服务有 ERROR/WARN，再选择服务查询 templates 或关联 Trace。只统计观测库中已存储的日志，不是服务注册目录。

## 请求

| 参数 | 必填 | 说明 |
| --- | --- | --- |
| `start` / `end` | 否 | Unix 秒；默认最近 1 小时，窗口上限 7 天，修正会写入 notices |
| `service` | 否 | 不传或空白时按所有匹配服务分别统计；指定时仅统计该服务，去除参数首尾空白 |
| `route` | 否 | 按路由精确过滤 |
| `method` | 否 | 按 HTTP 方法精确过滤 |

`level`、`top_n` 已移除，显式传入（包括空值）返回 HTTP 400。模板查询使用 `/logs/templates`。

## 响应

```json
{
  "window": {"start": 1787000000, "end": 1787003600},
  "summaries": [
    {
      "service": "user-service",
      "total": 100,
      "error_count": 10,
      "error_rate": 0.1,
      "by_level": {"DEBUG": 0, "INFO": 70, "WARN": 20, "ERROR": 10}
    },
    {
      "service": "gateway",
      "total": 80,
      "error_count": 0,
      "error_rate": 0,
      "by_level": {"DEBUG": 0, "INFO": 80, "WARN": 0, "ERROR": 0}
    }
  ],
  "generated_at": 1787003600,
  "notices": ["error_count/error_rate 按各服务匹配日志计算，不是失败请求数或请求失败率；未出现的服务不代表正常"]
}
```

- `window` 是实际生效的查询边界；仍使用已有秒转毫秒的 `ts BETWEEN start*1000 AND end*1000` 闭区间。相邻窗口边界可能重合，不能直接相加。
- `summaries` 始终为数组，指定 service 也如此；没有匹配日志返回 `[]`，不虚构零日志的服务行，不额外返回全局 summary。
- 每个服务的 `total` 为所有匹配日志数，`error_count` 为 ERROR 日志数，`error_rate=error_count/total`，四舍五入保留 4 位小数。它是日志占比，不是请求失败率；同一次请求可能记录多条 ERROR，不能除以二估算请求数。
- `by_level` 固定包含 DEBUG/INFO/WARN/ERROR，未出现的级别填 0；额外级别也保留并计入 total。
- 按 ERROR 数降序、WARN 数降序、service 升序排列，服务分组不截断。route/method 限制后的结果只代表该筛选范围。
- 缺失或全空白 service 归入 `service: ""` 并提示无法确认归属，不猜测服务名。指定有效 service 不包含缺失归属日志。
- 数据库失败返回 HTTP 500，不能解释为没有异常。无日志也可能是未采集或查询条件不匹配。

## 实现与验证

数据库一次按 `(service, level)` 分组；应用按服务汇总并排序，不再额外查询 Top 模板。相关模型在 `internal/logstore/log_model.go`。

运行 `go test ./...`。store 测试通过 database/sql 驱动夹具验证分组行处理、参数绑定、空结果和失败传播；handler 测试验证响应契约与参数校验。数据库执行计划和真实 MySQL 执行需在部署环境另行验证。
