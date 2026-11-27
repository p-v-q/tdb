# 竞品差距分析与路线图

本文把 TDB 与 DBeaver、DataGrip、SQL Server Management Studio（SSMS）/ Azure Data Studio、Bytebase 对照，标注 TDB 已具备 / 部分具备 / 缺失的能力，并给出向"可商用 + 开源就绪"推进的优先级。评估口径：**终端 TUI 客户端**的可行形态，不追求复刻 GUI 专有功能（如拖拽 ER 图、图形化执行计划画布）。

数据来源：各产品公开特性页与文档（DBeaver features / docs、DataGrip features / docs、Bytebase docs），以及 TDB 当前实现（见 `docs/architecture.md` 与 README Roadmap）。

## 能力对照矩阵

| 能力 | DBeaver | DataGrip | SSMS/ADS | Bytebase | TDB 现状 |
| --- | --- | --- | --- | --- | --- |
| 多数据库连接 + 连接管理 | ✅ | ✅ 50+ | ✅ | ✅ | ✅ SQLite/PG/MySQL/MariaDB |
| 元数据浏览（表/视图/函数/触发器/索引/序列） | ✅ | ✅ | ✅ | ✅ | ✅ 能力感知分组 |
| 查看对象 DDL | ✅ | ✅ | ✅ | ✅ | ✅ PG 还原主键/外键/CHECK/identity |
| SQL 高亮 / 格式化 | ✅ | ✅ | ✅ | ✅ | ✅ 含复合语句保护 |
| 上下文补全 | ✅ | ✅ 智能 | 部分 | ✅ | ✅ 含 DDL 位置 |
| 执行计划 | ✅ | ✅ 表/图/火焰图 | ✅ 图形 | — | ⚠️ 纯文本 |
| 结果网格（排序/过滤/复制/导出） | ✅ | ✅ | ✅ | ✅ | ✅ CSV/TSV/JSON/Markdown |
| **数据编辑（改单元格 → 生成 UPDATE）** | ✅ | ✅ | ✅ | — | ✅ 单表 SELECT + 主键可编辑，确认后参数化执行 |
| 事务控制（自动/手动提交、回滚） | ✅ | ✅ | ✅ | — | ✅ 手动提交 + Commit/Rollback + 未提交指示 |
| **只读模式 + 危险语句确认** | ✅ | ✅ | 部分 | ✅ | ✅ |
| 密码安全存储（keychain） | ✅ | ✅ | ✅ | ✅ | ⚠️ 连接信息可整体用主密码加密（AES-256-GCM + PBKDF2），系统 keychain 待做 |
| 配置静态加密（主密码 / 提示问题） | ✅ | ⚠️ | ✅ | ✅ | ✅ 整份连接列表加密，锁定时文件无任何连接信息 |
| SQL 生成（SELECT/INSERT/UPDATE/DELETE/DDL） | ✅ | ✅ | ✅ | ✅ | ✅ 右键对象树生成 + View data |
| 对象名过滤 / 全局搜索 | ✅ | ✅ | ✅ | ✅ | ⚠️ 对象树 `/` 过滤已具备，跨对象全局搜索未做 |
| 数据内搜索（跨表查值） | ✅ | ✅ | ✅ | — | ❌ 未实现 |
| 即时语法检查 / lint | ✅ | ✅ 实时 + quick-fix | 部分 | ✅ 200+ 规则 | ❌ 未实现 |
| 查询参数化 | ✅ | ✅ | ✅ | ✅ | ❌ 未实现 |
| 导入数据（CSV → 表） | ✅ | ✅ | ✅ | — | ❌ 未实现 |
| 导出格式（CSV/JSON/Markdown/HTML） | ✅ | ✅ | ✅ | ✅ | ⚠️ CSV/TSV/JSON/Markdown（HTML 未做） |
| 多结果集 / 查询历史 | ✅ | ✅ | ✅ | ✅ | ✅ 历史可过滤 |
| 结果集比较 | ✅ | ✅ Diff | ✅ | — | ❌ |
| ER 图 | ✅ | ✅ | 部分 | — | ❌ |
| 变更工作流 / 审批 / 回滚 | — | — | — | ✅ | ❌（平台能力，非目标） |
| SQL 审计日志 | ✅ | ✅ | ✅ | ✅ | ⚠️ 仅查询历史 |
| SSH 隧道 / TLS 配置 | ✅ | ✅ | ✅ | ✅ | ⚠️ DSN 透传 |
| 快捷键 / 鼠标 / i18n | ✅ | ✅ | ✅ | ✅ | ✅ en/zh + 快捷键浮层 |

图例：✅ 已具备 · ⚠️ 部分 · ⏳ 进行中 · ❌ 缺失

## 优先级路线图

### P0 —— 商用可用性的硬门槛

这些缺一项，产品就不敢在真实生产库上用。

1. ✅ **数据编辑**（已完成）：结果网格里可改单元格（仅单表 SELECT + 有主键 + 非只读），待提交修改有标记与计数，应用前弹确认框展示参数化的 UPDATE，手动提交模式下可回滚。设计见 `docs/ux-review-round4.md`。
2. ✅ **事务控制**（已完成）：自动提交 / 手动提交切换、Commit / Rollback（F9 / F11）、未提交事务的醒目状态提示。
3. ✅ **查看对象 DDL**（已完成）：SQLite / PostgreSQL / MySQL 均可在新标签打开 `CREATE` 语句，PG 还原主键、外键、CHECK、identity。
4. **密码进系统 keychain**（部分完成）：连接信息现在可以整体用主密码加密（PBKDF2-HMAC-SHA256 600k + AES-256-GCM，见 `docs/secrets-encryption.md`），锁定时 `settings.json`、`.bak` 与 `-print-settings` 都不含任何连接字段；也支持从环境变量读取密码。系统 keychain（macOS Keychain / Windows Credential Manager / Linux Secret Service）仍待做，用于免去每次输入主密码。
5. ✅ **危险语句护栏**（已完成）：只读模式 + 无 WHERE 的 UPDATE/DELETE 与 DDL 确认。
6. **连接健壮性**（已完成）：连接与元数据加载 15 秒超时、Reconnect / Disconnect、连接丢失提示、损坏设置文件自动备份、每连接默认 schema（PostgreSQL `search_path`）。

### P1 —— 与主流客户端平齐的效率能力

7. ✅ **SQL 生成**（已完成）：右键对象树生成 SELECT / INSERT / UPDATE / DELETE，以及 View data。
8. ⚠️ **对象过滤 / 全局搜索**：对象树 `/` 过滤已完成（名称、限定名、签名、注释）；跨对象全局搜索未做。
9. **导入数据**：CSV → 表（列映射、类型推断、批量提交）。
10. ⚠️ **更多导出格式**：JSON / Markdown 已完成，HTML 未做。
11. **即时 lint**：复用 `sql` 包词法，在编辑器状态栏提示未闭合引号、缺 FROM、危险语句；对齐 Bytebase 的 SQL Review 理念但保持轻量。
12. **查询参数化**：`:name` 参数提示输入，复用历史。
13. **执行计划增强**：EXPLAIN 输出结构化展示（树形缩进），并支持 EXPLAIN ANALYZE。
14. **结果网格多选与复制格式**：标记多行后复制为 SQL / JSON / Markdown / CSV（竞品 lazysql 的做法）。
15. **插入 / 删除行**：网格里新增行、按主键删除行（与单元格编辑同一套待提交与确认机制）。
16. **行数统计**：结果集显示精确/估算行数（竞品会区分 “843 rows” 与 “~4.3M rows”）。

### P2 —— 差异化与深度

15. 结果集比较（两个结果集 Diff）。
16. 数据内搜索（在多张表里查某个值）。
17. ASCII/Unicode ER 图（TUI 可画关系连线）。
18. SQL 审计日志（比查询历史更完整的操作留痕）。
19. SSH 隧道配置 UI（当前仅 DSN 透传）。

### 明确不做（非 TUI 单机客户端目标）

- Bytebase 式变更工作流 / 多级审批 / 多环境 rollout（那是平台能力）。
- 图形化拖拽查询构建器、可视化执行计划画布。
- 备份恢复、任务调度（依赖外部工具链）。

## 开源就绪清单

向开源发布还需补齐（与功能路线正交）：

- `LICENSE`（Apache-2.0）、`CODE_OF_CONDUCT.md`（`CONTRIBUTING.md`、`SECURITY.md` 已有）
- `CHANGELOG.md` 与语义化版本发布流程（已完成：当前版本 0.9.1，CHANGELOG 按 Keep a Changelog 维护）
- CI 矩阵已补 PostgreSQL / MySQL 集成测试（`ci.yml` 的服务容器 + DSN 守卫）；独立的 PTY 场景套件仍需本地数据库，见 `docs/integration-testing.md`
- 测试覆盖率门槛与 `golangci-lint`（仍待做）
- README 截图 / 演示 GIF、架构文档（已完成：真实终端录制与 4 张截图）
- Issue / PR 模板

## 与既有 Roadmap 的关系

本文不替代 README 的 Roadmap，而是给它提供取舍依据。P0 项应优先于 README 里尚未勾选的其余条目；P1/P2 按用户价值逐批推进。

## 0.9.0 已知限制（发布审查后保留）

以下问题不在 0.9.0 范围内，已记录以便后续版本处理：

- PostgreSQL 重载函数/过程：`DefinitionRef` 没有签名，同名重载可能打开另一个重载的定义（同 kind 已按函数/过程过滤）。
- PostgreSQL `BEGIN ISOLATION LEVEL …` / `START TRANSACTION READ ONLY` 会被当作事务开始，但隔离级别/只读选项不生效。
- PostgreSQL 分区表/继承表的 DDL 仍输出普通 `CREATE TABLE`；物化视图 reloptions、identity 序列选项、列级存储属性未还原。
- 生成的 SQLite 列会列在对象树中，但目录没有“只读列”标记，网格仍可能让人尝试编辑。
- 尚未保存的 SQL 编辑器内容没有 dirty 跟踪：关闭标签/退出时只会为“未应用的网格编辑”询问，不会为未保存的 SQL 询问。
- 新的查询结果或“清空结果”仍会直接替换未应用的网格编辑（关闭标签/退出/切换连接已有确认）；断开连接仍按原语义直接丢弃。
- MySQL 未启用 `clientFoundRows`：把单元格改回等值但不同字面量（如 INT 的 `01`→`1`）时 affected==0，会继续显示为未应用。
- 系统 keychain、CSV 导入、跨对象全局搜索、查询参数提示、SQL lint、结果集对比、ER 图、SSH 隧道配置 UI 仍在路线图中（见上）。