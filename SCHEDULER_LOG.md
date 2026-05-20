# Scheduler Log — `sync-2api-models-daily`

Job 路径: `/root/.config/opencode/scheduler/scopes/cliproxyapi-152cff5cf729/jobs/sync-2api-models-daily.json`
调度: 每天 00:00 (cron)
工作目录: `/mnt/sdb1/code2api/CLIProxyAPI`

---

## 运行历史

| 日期 | 状态 | 耗时 | 说明 |
|------|------|------|------|
| 2026-05-17 | ✅ success | 76s | 模型已同步，5 个模型均在配置中，无需变更 |
| 2026-05-18 | ✅ success | 115s | 同上，无变化 |
| 2026-05-19 | ✅ success | 95s | 同上，无变化 |
| 2026-05-20 | ❌ failed | 3s | `opencode` 二进制路径失效 (`/root/.nvm/.../bin/opencode` 不存在) |
| 2026-05-21 | ✅ success | — | 修复后手动验证，执行成功 exitCode=0 |

## 故障与修复

### 2026-05-20: cron 条目缺失 + 二进制路径失效

**根因**: 两个问题叠加:
1. **crontab 条目缺失** — `sync-2api-models-daily` 注册时未写入 crontab，导致 cron 从不触发
2. **二进制路径不对** — 残留旧路径 `/root/.nvm/versions/node/v22.18.0/bin/opencode`，该路径已不存在

**修复**:
1. 添加 crontab 条目 (与其余 3 个 job 格式一致)
2. 确认 job 配置 `command` 已正确指向 `/usr/bin/opencode` (符号链接 → `/mnt/sdb1/opencode/opencode`)

### 2026-05-21: 功能扩展

**变更**: 将 job 从纯模型同步扩展为每日维护流水线:

| 步骤 | 操作 |
|------|------|
| ① | Sync dev → main (fetch upstream → reset main → cherry-pick dev → force-push) |
| ② | `rebuild.sh` (编译二进制 → 构建 Docker → 重启容器) |
| ③ | Sync 2api models (检查模型列表 → 更新 opencode.json / oh-my-openagent.json) |

**冲突安全机制**: cherry-pick 遇冲突时自动 `--abort` 并退出，不 rebuild，不 force-push。

**超时**: 300s → 600s

## 当前状态

- **Service**: ✅ `cli-proxy-api` Docker 容器运行中
- **2api 模型**: 5 个 (`astron-code-latest-xfyun`, `deepseek-v4-pro-deepseek`, `deepseek-v4-flash-deepseek`, `MiniMax-M2.7-highspeed-minimax`, `MiniMax-M2.5-highspeed-minimax`)
- **opencode.json**: ✅ 已同步
- **oh-my-openagent.json**: ✅ 已同步 (2 个 `2api/*` 引用均有效)
