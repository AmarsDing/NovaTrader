# 文档已迁移

本仓库的 `doc/` 资料文档已于 **2026-11-08** 迁移至工作区文档中枢：

- `doc/开发文档/design/` → `../docs/NovaTrader/design/`（**前端规范 canonical 位置**，含 01–05 规范与 `assets/aether-v2-2026-10-30/`）
- `doc/开发文档/M00–M14 各模块/` → `../docs/NovaTrader/design/modules/`
- `doc/开发文档/实施记录/` → `../docs/NovaTrader/guide/implementation/`
- `doc/开发文档/研究资料/` → `../docs/NovaTrader/research/`
- `doc/开发文档/部署/` → `../docs/NovaTrader/guide/deploy/`
- `doc/开发文档/assets/` → `../docs/NovaTrader/design/assets/`
- `doc/开发文档/视频/` → `../docs/NovaTrader/reference/media/`
- `doc/开发文档/2.需求规格说明书.md`、`4.开发进度跟踪.md`、`doc/NovaTrader智脑方案.md` → `../docs/NovaTrader/plan/`
- `doc/开发文档/` 顶层其余方案文档 → `../docs/NovaTrader/design/`
- `doc/RAG.md` 及剩余目录 → `../docs/NovaTrader/reference/`

规则见工作区根目录 `AGENTS.md` 第 7 节「文档中枢」。前端规范引用路径以 `docs/NovaTrader/design/` 为准（见 AGENTS.md 第 6 节）。新增资料文档请直接写入 `../docs/NovaTrader/<类型>/`，不要在本仓库重建 `doc/`。

注意：`web/src/theme/tokens.css` 为源码内令牌唯一来源，留在本仓库未迁移。
