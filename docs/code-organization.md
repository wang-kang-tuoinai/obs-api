# 代码与测试目录

## 业务代码

`main.go` 负责配置、依赖初始化及服务启动，`internal/router/router.go` 负责注册路由。

`internal/handler` 按接口功能拆分文件，仍属于同一个 `handler` 包：

| 文件 | 职责 |
| --- | --- |
| `log_handler.go` | 日志 Handler 定义与构造函数 |
| `log_stats.go` | 日志统计接口 |
| `log_templates.go` | 日志模板接口 |
| `log_search.go` | 日志搜索接口 |
| `trace_handler.go` | Trace Handler 定义与构造函数 |
| `trace_stats.go` | Trace 统计接口 |
| `trace_search.go` | Trace 搜索接口 |
| `trace_detail.go` | Trace 详情接口 |
| `trace_response.go` | Trace 统计、搜索响应模型 |
| `visual_handler.go` | 可视化 Handler 定义、服务目录接口约定与构造函数 |
| `visual_services.go` | 可视化服务目录接口 |
| `visual_traces.go` | Trace 可视化接口 |
| `visual_logs.go` | 日志可视化接口 |
| `time_range.go` | 日志与 Trace 工具共用的秒级时间窗口解析 |

`internal/router` 负责注册路由：

| 文件 | 职责 |
| --- | --- |
| `router.go` | `SetupRouter` 注册日志、Trace 与可视化接口路由 |

`internal/logstore` 负责日志存储查询、聚合与可视化缓存：

| 文件 | 职责 |
| --- | --- |
| `store.go` | `LogStore` 接口：统计 / 模板 / 搜索三个查询方法 |
| `log_model.go` | 日志查询与响应模型（`StatsQuery`、`StatsSummary`、`TemplatesResponse`、`SearchQuery`、`LogItem` 等） |
| `mysql.go` | MySQL 实现：按服务/级别聚合统计、模板窗口函数查询、游标分页搜索 |
| `histogram.go` | 日志时间桶聚合：按 10 秒桶 `GROUP BY level`，首尾桶裁剪到查询窗口 |
| `visual.go` | 日志可视化缓存：单 worker 异步刷新 + 快照读穿透，支持滚动/固定窗口 |

`internal/tracestore` 负责 Jaeger 适配、Span 建树、入口选择、分析、聚合与可视化缓存：

| 文件 | 职责 |
| --- | --- |
| `provider.go` | 领域模型与接口：`Span` / `Trace` / `TraceProvider` / `TraceBatch` |
| `jaeger_model.go` | Jaeger API 原始 JSON 模型（trace / span / tag / log / process / reference） |
| `jaeger.go` | Jaeger HTTP 适配：`JaegerProvider` 拉取 trace、operation、service 并转成 `Span` |
| `tree.go` | 多根森林构建：副本校验、环检测、父子排序、孤立片段 |
| `analyze.go` | Trace 分析：状态分类、错误判定、`self_ms`、错误源头定位 |
| `entries.go` | 入口选择：`ServiceEntry` 与 `SelectServiceEntries` 供统计/搜索共用 |
| `expected_error.go` | 预期错误豁免：标记 `handled_mysql_duplicate_key` 不记为错误 |
| `search.go` | Trace 搜索：DFS 匹配 server 入口，生成摘要与错误摘要 |
| `stats.go` | Trace 聚合：按服务/接口分组，计算百分位与下游错误服务 |
| `stats_query.go` | 统计编排：`CollectStats` 按 operation 分预算并发采集 |
| `detail.go` | Trace 详情展示模型与先序裁剪（`TraceDetail` / `SpanDetail` / `BuildDetail`） |
| `visual.go` | Trace 可视化缓存：单 worker 异步刷新、快照、散点上限截断 |

以上各包的职责与业务逻辑在本次拆分中保持不变。

## 测试代码

- `tests/handler`：HTTP 接口契约测试，通过公开构造函数和 Gin 路由验证参数、响应及错误处理；使用测试替身和本地 HTTP 测试服务器，无需连接真实 MySQL 或 Jaeger。
- `internal/logstore/*_test.go`、`internal/tracestore/*_test.go`：包内单元测试，保留在被测包旁边。它们需要访问未导出的函数、类型或缓存字段，例如 `computeSelfMs`、`classifyStatus`、缓存刷新状态。

Go 按目录划分包，即使子目录声明了相同的包名，也不能访问原包的私有成员。因此不为移动测试而扩大生产代码的公开接口。`*_test.go` 不会编入正常构建的服务程序。

在 `obs-api` 根目录运行全部测试：

```sh
go test ./...
```

单独运行 HTTP 契约测试：

```sh
go test ./tests/handler
```

运行全部测试并检查数据竞争（需要环境支持 race 检测）：

```sh
go test -race ./...
```

HTTP 测试已独立成包；如需将其调用的生产代码计入覆盖率，使用：

```sh
go test -coverpkg=./internal/... ./...
```
