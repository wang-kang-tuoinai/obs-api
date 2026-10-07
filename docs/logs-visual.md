# 日志可视化快照

`GET /api/v1/visual/logs` 专供观测面板；Agent 的 logs/stats、templates、search 保持原接口。

## 查询

```text
/api/v1/visual/logs?service=ops-agent-backend
/api/v1/visual/logs?service=ops-agent-backend&method=PUT&route=%2Fapi%2Fv1%2Fusers%2F%3Aid
/api/v1/visual/logs?service=ops-agent-backend&start_ms=1791000000000&end_ms=1791000900000
```

- service 必填，去首尾空格，最多 64 字节。
- method/route 同时省略或同时非空。method 为标准大写 HTTP 方法，route 为 `/` 开头的路由模板，最多 255 字节。
- start_ms/end_ms 同时省略为实时最近 15 分钟；成对提供为历史窗口，长度大于 0 且不超过 15 分钟。允许 5 秒未来时间容忍。
- 不接受 operation、level、limit 或任意桶宽。桶宽固定 10000ms，返回所有日志级别。
- 非法参数 400，缓存全忙且没有可淘汰条目 429；快照正常返回 200，包括后台加载/失败状态。Cache-Control 为 no-store。

## 响应字段

| 字段 | 含义 |
| --- | --- |
| service/method/route | 实际过滤条件 |
| window | 本次访问的观察窗口，实时模式随当前时间前移 |
| data_window | 最后一次成功聚合的真实时间范围；未成功为 null |
| bucket_ms | 10000 |
| buckets | 按时间排序，包含 start_ms/end_ms/total/by_level |
| summary | data_window 内所有桶的 total/by_level 总和；未成功为 null |
| initialized | 是否至少成功查询过一次 |
| loading | 后台任务排队或执行中 |
| stale | 未初始化、查询失败或快照过期 |
| error | 查询失败信息，可省略 |
| updated_at_ms | 最近一次成功更新缓存的时间，未成功为 0 |
| data_as_of_ms | data_window 的末端，未成功为 0；不是入库完整性水位 |
| refresh_seconds | 15 |
| notices | 边界桶、缓存覆盖范围等提示 |

窗口与桶采用 `[start_ms,end_ms)`。桶按 Unix 时间 10 秒边界对齐，首尾裁切到查询窗口，因此最多 91 个桶。补零只在 SQL 成功后进行。DEBUG/INFO/WARN/ERROR 默认存在，未知级别也保留，前端汇入“其他”。

首次加载 buckets=[]、summary=null，不代表零日志；成功零结果包含已补零的桶。后台查询失败保留旧 buckets、summary、data_window，并设置 error/stale。读取旧缓存不会重写其数据范围。日志条数不是失败请求数。

现有 Agent 日志接口是秒级 BETWEEN 查询，末端恰好相等的毫秒记录可能与本接口计数有边界差异；本次未修改其协议。

## 缓存

进程内有界缓存，最多 8 份；键包含服务、method/route、实时/历史模式及历史起止时间。实时键不包含当前时间。每条仅保存聚合结果，不保存日志正文。

首次请求立即返回快照并排队。日志专用单 worker 查询数据库，SQL 超时 5 秒；同一条目不重复排队。实时条目最近 1 分钟内被访问时，至少每隔 15 秒尝试刷新一次。每次重新聚合整个 15 分钟窗口，成功后整体替换。

历史条目被访问时，成功结果至少复用 60 秒；失败重试最短间隔 15 秒。实时数据截止落后超过 30 秒、历史快照查询年龄超过 120 秒时标记 stale。

未访问 1 分钟后停止实时自动刷新，10 分钟后淘汰；容量满时淘汰未加载的最久未访问条目。日志 worker 不与 Trace worker 共用队列。关闭时取消后台 SQL，再释放数据库。

## 索引与部署

业务后端 LogEntry 模型新增 `idx_svc_ts(service, ts)`，启动时通过现有 obs-mysql AutoMigrate 创建。重建 obs-api、rag-gateway，以及负责日志表迁移的 app：

```sh
docker compose up -d --build --no-deps app obs-api rag-gateway
```

上述命令假设所需依赖已运行。实际数据库应检查 `SHOW INDEX FROM observability.logs` 中的 idx_svc_ts；若采用手动迁移，确认索引不存在后对 obs-mysql 执行：

```sql
CREATE INDEX idx_svc_ts ON observability.logs (service, ts);
```

可对真实聚合查询使用 EXPLAIN 检查索引和扫描量；本地单元测试不证明实际 SQL 耗时或索引已部署。新增索引会占用空间并增加写入维护开销，现有索引保留。

## 前端与验证

同源网关仅代理 GET /api/v1/visual/logs。观测面板共享服务、HTTP operation 和显示时间轴；标准 operation 映射为日志 method/route。非 HTTP operation 明确提示不支持日志联动。

两图独立读取快照与显示错误；首次查询进行中约 2 秒轮询，初始化后约 15 秒读取。拖动期间冻结显示时间轴，完成后两图共享绝对选区。灰色区域表示缓存尚未覆盖，不能当成零；边缘裁切不改变桶的原始计数。草稿兼容原 Trace 范围块，替换为观测范围块。

```sh
# obs-api
go test -race ./...
# rag-gateway
go test -race ./...
node --test tests/frontend.test.mjs tests/trace-panel.test.mjs
```

使用模拟 SQL driver 验证一次分组查询、参数过滤、补零和边界；缓存测试覆盖重复请求合并、快照拷贝、失败保留结果、取消、容量与空闲淘汰。浏览器预览使用合成数据，不连接实际 MySQL 或 Jaeger。
