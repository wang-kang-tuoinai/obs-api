# Trace 可视化第一版

页面入口：rag-gateway 右上角「Trace 面板」。日志面板、Span 瀑布图尚未实现。

## 查询 API

`GET /api/v1/visual/services` 返回 Jaeger 服务目录及 `default_service`，默认服务由 `TRACE_ENTRY_SERVICE` 配置。目录不是健康状态，也不按时间窗口筛选；目录请求失败返回 502，页面仍允许手动输入服务名。

`GET /api/v1/visual/traces`

| 参数 | 说明 |
| --- | --- |
| service | 必填，首尾去空白，最多 128 字节 |
| operation | 可选，最多 512 字节；仅过滤缓存，复用同服务同窗口的查询任务 |
| start_ms / end_ms | 两者同时省略为实时最近 15 分钟；同时提供为固定历史范围，单位毫秒，范围大于 0 且不超过 15 分钟，不允许未来窗口（5 秒时钟容差） |

参数错误返回 400。缓存都在执行且已达到容量时返回 429。

首次访问立即返回 JSON，`initialized=false, loading=true, points=[]`；后台完成后轮询得到结果，不阻塞 HTTP 等待整批 Trace。失败也返回含状态的快照；前端必须读取 `error/stale/partial`，不能把空数组当作服务健康。

主要响应字段：

| 字段 | 口径 |
| --- | --- |
| window.start_ms / end_ms | 本次展示的时间范围，毫秒；实时模式会滚动 |
| points | trace_id、entry_span_id、service、operation、start_ms、duration_ms、status、incomplete |
| operations | 最近取得的 server 操作目录，不保证窗口内均有请求 |
| by_status | 当前筛选后缓存样本的 ok/degraded/failed 计数，不是全量流量指标 |
| initialized / loading | 是否曾得到可用查询结果，以及是否排队或刷新中 |
| stale | 尚未就绪、整轮失败或实时查询截止已落后超过 30 秒 |
| partial | 已知存在候选截断、操作失败/跳过、链路警告或缓存裁剪；false 仍不保证全量采集 |
| updated_at_ms | 最近一次有可用查询结果的完成时间，未就绪为 0 |
| data_as_of_ms | 最近一次有可用结果的查询截止，部分操作失败时不能理解为所有操作都已更新 |
| operation_queries | 最新查询各 operation 的 success/failed/skipped、raw_trace_count、limit_reached；之前缺失提示可能仍保留在 notices |
| notices / error | 不完整提示、整轮失败原因 |
| refresh_seconds / point_limit | 刷新间隔与单缓存点数上限 |

所有时间字段标明单位；请求与图表内部为毫秒，填入 Agent 草稿时开始向下取整到秒、结束向上取整到秒。

## 查询与缓存生命周期

- 缓存键为 `(service, 固定窗口起止)`；实时窗口使用独立标识。operation 不是缓存键。
- 全进程一个后台工作任务，operation 串行查询，Agent 的 stats/search 查询不受此队列调度。
- 首次查询 15 分钟，各 operation 默认最多 1500 条原始候选，沿用 `TRACE_STATS_PER_OPERATION_LIMIT`；单轮最多查询 30 个 operation，总预算 90 秒，单次 Jaeger HTTP 超时仍为 30 秒。
- 每个 operation 的完整 Trace 经现有 BuildTrace、SelectServiceEntries、classifyStatus 转成标量摘要后，不再由缓存持有树引用。暂未另写简化建树算法，重复键排除、多根和孤儿入口口径与现有工具一致。
- 入口按 `(trace_id, entry_span_id)` upsert；同一 Trace 多次进入服务保留多个点。重复命中的入口状态可以随迟到 Span 更新。
- 活跃实时缓存约每 15 秒重新查询最近 2 分钟；每 5 分钟全窗口校正。查询串行、耗时较长或排队时实际周期可能更长，不会叠加执行。
- 离上次查询截止超过 2 分钟，改做完整窗口查询；新发现的 operation 也查询整个窗口，避免只补最近 2 分钟。
- 未返回某个旧入口不作为删除依据；仅按窗口淘汰或容量裁剪。整轮失败保留已有摘要，显示旧数据及错误。
- 增量成功不清除之前的查询缺口提示，完整窗口查询无已知限制时才清除。候选触顶时缩小历史窗口；选择 operation 本身不使用 5000 候选补查预算。
- 无访问超过 1 分钟停止为其安排新实时刷新；闲置超过 10 分钟删除缓存。最多 8 个窗口缓存，满时淘汰未执行中的最近最少使用项。
- 每个窗口最多 20000 个入口摘要，超出保留较新的样本并明确标记 partial；没有抽样为完整分布的假象。
- 历史窗口不随当前时间滚动；访问时最多每分钟重新查询一次，便于补齐近期迟到数据。

后台内存只长期持有摘要。但单批 Jaeger JSON 与树仍有临时内存开销，候选数并不限制单条 Trace 的 Span 数；上限并非进程内存硬限制。多副本各自维护内存缓存，重启需要重新加载，未引入 Redis 或新数据表。

## 前端交互

- Canvas 绘制所有返回点，最多 20000 个，按状态着色，文字图例附计数；不是为每个点创建 DOM 元素。
- 悬停显示 service、operation、时间、耗时、状态、Trace/入口 ID；空结果、首次加载、过期和失败明确区分。
- 图上拖动框选，或者键盘输入起止时间，填入当前对话草稿中的 `[Trace 诊断范围]` 块。再次选择替换范围块，保留用户其他文字，不自动发送。
- 正在生成或提交待确认时不覆盖草稿，保留选区，稍后可点击「填入诊断范围」。
- 切换服务/接口/窗口取消旧请求，并通过请求代号丢弃迟到响应。关闭面板或切到后台标签页停止轮询。
- 图表查询每 15 秒轮询；后台加载期间每 2 秒读取缓存进度，不会每次轮询都触发新 Jaeger 查询。

## 验证

```powershell
# obs-api
go test -race ./...
go test ./internal/tracestore -run '^$' -bench BenchmarkVisual4500Traces -benchtime=1x -benchmem

# rag-gateway
go test -race ./...
node --test tests/frontend.test.mjs tests/trace-panel.test.mjs
node tests/preview-server.mjs
```

预览服务只使用内存合成数据，不调用模型、不写数据库。包括 4500 个点、接口筛选和 `offline` 服务失败场景。

本机一次合成基准：4500 条 Trace、每条 20 个 Span，BuildTrace 与状态判断约 44 ms，累计分配约 40.5 MB。**累计分配不是峰值/常驻内存；该基准不含 JSON 解码、Jaeger 查询和传输，也不是容器性能承诺。**

HTTP 夹具测试覆盖 Jaeger 格式解析及目标服务入口摘要；缓存测试覆盖重叠更新、同链路多入口、错误保留、长间隔全窗口补查、容量和并发共享；前端测试覆盖时间换算、草稿保留及迟到响应丢弃。真实部署延迟和实际 Span 规模需在现有故障流量下联调。
