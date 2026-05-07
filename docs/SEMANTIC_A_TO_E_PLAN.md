# AI 候选流水线方案状态

该历史方案已经从当前 CLI 支持面移除。

当前公开能力只覆盖：

- `mingest get <url> [--out-dir <dir>] [--name-template <tpl>] [--asset-id-only] [--json]`
- `mingest ls [--limit <n>] [--query <text>] [--format <table|json>] [--dedupe]`
- `mingest auth <platform>`
- `mingest -V|--version`

如未来重新引入 AI 候选相关能力，需要先重新确认产品边界、命令设计、隐私策略、模型依赖与验收标准。
