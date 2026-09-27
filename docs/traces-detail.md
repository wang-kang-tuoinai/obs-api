# Trace 详情工具

`GET /api/v1/traces/:trace_id`，Agent 工具名 `get_trace_detail`。

从 stats 找到入口，从 search 选择请求，再通过详情查看单次请求的调用树和各节点错误。已有日志中的 trace_id 时可直接查询。

## 参数

| 参数 | 说明 |
|---|---|
| trace_id | 必填路径参数，使用真实链路 ID |
| max_spans | 可选，默认 50，范围 1–200；非法值返回 400 |

## 响应

顶层保留 trace_id、root_operation、start_ms（根开始时间，Unix 毫秒）、duration_ms、status、span_count、root、warnings。省略原来的 error_origin 和 error_desc；各节点分别提供具体错误。

另有 returned_span_count（实际展示节点数）、truncated（是否因 max_spans 裁剪）、incomplete（已检测到缺失上游或无法确定唯一根）。span_count 是成功解析的节点总数。父节点缺失的独立子树保存在 fragments，不再丢弃；root 与 fragments 共用 max_spans 预算。incomplete=false 也不能证明采集端没有漏报。

节点保留 span_id、service、operation、kind、duration_ms、self_ms、status、status_desc、error、attrs、children。不返回节点 trace_id、parent_span_id 或绝对 start_ms，使用 start_offset_ms（相对根开始时间，毫秒）。children 表达父子关系。

status_desc 与 error 含义不同，分别保留：前者来自 SetStatus 的操作描述，后者来自 RecordError 的异常类型和消息。空值省略。attrs 复用 Jaeger 层已经清理的属性。

顶层 status 为全局根的 ok/degraded/failed 分类；无唯一全局根时为 unknown，root 省略，root_operation 为空、duration_ms 为 0（不代表实际耗时为零），使用 fragments 展示。此时 start_ms 与 start_offset_ms 参考最早片段开始时间。节点 status 为原始归一化 Span 状态 ok/error。

分类不再对 4xx 直接返回 ok，只排除已确认的 MySQL 重复键业务冲突，规则见 [stats 文档](traces-stats.md)。节点可包含 expected_error=handled_mysql_duplicate_key，解释该错误未参与分类/计数/摘要；原始 status、status_desc、error 保留。所有返回的正常、异常节点均保留，不仅展示异常节点。

search 返回的 entry_span_id 用于定位服务入口；detail 保留上游关系，不会将该服务入口重新设为全局 root。两者顶层耗时和状态可以不同。

## 耗时与完整性

self_ms = 父节点耗时 − 直接子节点在父区间内的时间区间并集。并行不重复扣除、越界区间裁剪、不重复扣除孙节点；结果保留三位小数。内部保留 Jaeger 微秒开始时间，展示仍使用毫秒。

self_ms 不是 CPU 时间，可能包含未埋点等待。最长节点或错误节点不自动等于根因。

节点按现有树顺序先序展示，达到 max_spans 后裁剪，保留祖先路径；可能省略错误节点，warnings 会提示。裁剪不重算状态和自身耗时，两者仍基于完整已解析树。可增大 max_spans 至 200；仍被裁剪时需在 Jaeger 中查看完整数据。节点数量限制不等于严格的 Token 上限。

本接口使用独立 TraceDetail/SpanDetail 投影，不修改共用 Trace/Span。Search 从目标入口子树重新选取错误摘要，不复用全局 ErrorOrigin/ErrorDesc。

## 错误响应

- 400：max_spans 非法，响应为 `{"error":"说明"}`。
- 404：未找到可返回的 Trace。
- 502：Jaeger 查询或解析失败。

请求示例：`GET /api/v1/traces/真实链路ID?max_spans=100`。
