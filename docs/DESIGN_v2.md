# Mingest 设计文档（v2）

## 1. 文档信息
- 项目：`mingest`
- 版本日期：2026-02-26
- 基线文档：`docs/DESIGN.md`（v1）
- 本版目标：在 v1 的下载/预处理/导出能力上，明确 “AI 候选 + 人工决策 + 质量闸门” 的可落地方向，并与当前代码实现对齐。

## 2. 结论先行
1. 产品主线仍是：`URL -> 下载 -> 预处理 -> 导出`。  
2. “全自动爆款切片”不作为当前承诺；可落地版本是：**AI 提候选 + 人工决策**。  
3. `doctor` 命令在 v2 的职责是：**导出前质量闸门**，而不是“预测爆款”。  

## 3. 产品定义（v2）
`mingest` 是面向创作者/代剪的本地 CLI：  
- 上游：解决下载与账户登录门槛。  
- 中游：沉淀可复用资产索引与预处理产物。  
- 下游：导出标准交付格式并在导出前进行质量校验。  

核心价值从“仅下载”升级为：**降低剪辑前后返工成本**。

## 4. 目标用户与痛点（更新）
### 4.1 目标用户
- 先拿 URL 再进入剪辑流程的创作者、代剪、轻协作团队。
- 主要工作流为 Premiere / Resolve，兼容 CapCut/剪映。

### 4.2 核心痛点（v2 优先级）
1. 素材组织与复用效率低（多项目、多素材易混乱）。
2. 字幕与切片返工高（边界切断、时间轴漂移、重复度高）。
3. 导出前缺少“质量闸门”（坏片段直接进入 NLE，导致二次返工）。

### 4.3 不主攻痛点
- 纯硬件性能瓶颈。
- 平台算法分发与推荐波动。
- 任何 DRM 绕过相关能力。

## 5. 产品策略：从“自动爆款”到“稳定落地”
### 5.1 不做的承诺
- 不承诺“AI 自动选出 3 段必爆高光”。
- 不承诺“全自动无人工确认”。

### 5.2 做的承诺
- 提供稳定候选与结构化结果。
- 提供人工决策入口（用户最终确认）。
- 提供 `doctor` 质量闸门，降低明显无效切片进入导出的概率。

## 6. 命令边界（当前实现）
- `mingest auth <platform>`
- `mingest get <url> [--out-dir] [--name-template] [--asset-id-only] [--json]`
- `mingest ls [--limit] [--query] [--format table|json] [--dedupe]`
- `mingest prep <asset_ref> --goal <subtitle|highlights|shorts> [...] [--json]`
- `mingest export <asset_ref> --to <premiere|resolve|capcut> [--with ...] [--out-dir] [--zip] [--json]`
- `mingest doctor <asset_ref> [--target <youtube|bilibili|shorts>] [--strict] [--json]`
- `mingest semantic <asset_ref> [--target <youtube|bilibili|shorts>] [--provider <auto|openai|openrouter>] [--model <name>] [--apply] [--json]`

说明：`asset_ref` 支持 `asset_id` 或本地路径。

## 7. 核心流程（v2）
### 7.1 基础流水线
`auth -> get -> ls -> prep -> semantic -> doctor -> export`

### 7.2 `prep` 的定位
- 生成 clips 候选与字幕相关产物（`prep-plan.json`、`markers.csv`、`subtitle*`）。
- 当前 clips 生成仍以规则法为主（均匀采样），可作为基础候选。

### 7.3 `doctor` 的定位
`doctor` 对 `prep-plan` 做导出前检查，当前覆盖：
- clips 数量、时间戳合法性、越界
- 片段时长范围、片段重叠
- 字幕来源（真实/模板）
- 字幕覆盖率、边界切断率
- 片段近重复度
- 均匀采样模式告警（提示切换语义候选流程）

### 7.4 `semantic` 流程（A-E）
1. Stage A：规则生成候选窗口（基于字幕）。
2. Stage B：GPT 语义重排（支持 OpenAI / OpenRouter）。
3. Stage C：约束选段 + 自动补位。
4. Stage D：生成评审包（`review.html` + 决策模板 + 预览视频）。
5. Stage E：可选写回 `prep-plan`，并执行 `doctor` 质量闸门。

## 8. 数据模型与目录规范（延续 v1）
### 8.1 资产索引
- `<appStateDir>/assets-v1.jsonl`
- 字段：`asset_id/url/platform/title/output_path/created_at`

### 8.2 预处理产物
- `.../.mingest/prep/<asset_id>/<timestamp>/prep-plan.json`
- `.../.mingest/prep/<asset_id>/<timestamp>/markers.csv`
- `.../.mingest/prep/<asset_id>/<timestamp>/subtitle.srt`（可用时）
- `.../.mingest/prep/<asset_id>/<timestamp>/subtitle-template.srt`

### 8.3 导出产物
- `.../.mingest/export/<asset_id>/<timestamp>/...`
- 可选：同名 `.zip`

## 9. 质量与验收（v2）
### 9.1 工程验收
- `doctor` 可稳定输出 `PASS/WARN/FAIL`（人读与 JSON 两种）。
- 失败场景可机器识别（退出码 `41`）。

### 9.2 业务验收（建议指标）
- `first-pass accept rate`：候选片段一次通过率。
- `edit time saved`：找片段时间下降比例。
- `rework count`：返工次数下降比例。

备注：v2 强调“效率收益可验证”，不以“爆款率”作为单一指标。

## 10. 风险与取舍
### 10.1 风险
- 字幕质量差会直接影响语义候选质量。
- “高光”定义存在主观差异，跨垂类泛化有限。
- 过度追求争议内容有平台风险。

### 10.2 取舍
- 先交付稳定半自动流程，再讨论更强自动化。
- 先做 CLI 与结构化产物，不先做重前端平台。

## 11. 路线图（建议）
### P0（已落地）
- `get / prep / export / ls / doctor / semantic` 主流程可用。

### P1（下一步）
- `semantic` 的评审交互增强（更顺滑的人机确认）。
- `doctor` 增加更多发布目标规则（如 shorts 专项）。
- 候选质量评估报表与长期指标闭环。

### P2（延后）
- 批量任务编排增强（更强并发/续跑策略）。
- 更复杂的跨平台改版自动化。

## 12. 一句话定位（v2）
`mingest` 不是“自动爆款机器”，而是一个面向创作者的 **可验证、可回滚、可导出的剪辑前后处理流水线**。
