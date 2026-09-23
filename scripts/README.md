# scripts/

一次性运维脚本，均为独立 `main` 包，在仓库根目录用 `go run ./scripts/<name>` 执行。
数据库配置与主线服务一致：读仓库根目录 `.env` 或进程环境变量 `DB_HOST` / `DB_PORT` /
`DB_USER` / `DB_PASSWORD` / `DB_NAME`。

| 脚本 | 作用 | 默认值 |
| --- | --- | --- |
| `credential-rekey` | 把 `vm_credentials` 的 SSH 凭据从旧主密钥重加密到新的 `CREDENTIAL_MASTER_KEY` | dry-run，`--apply` 写库 |
| `purge-task-secrets` | 清除 `tasks.payload` 里历史遗留的 SSH 明文口令 | dry-run，`--apply` 写库 |

## 建议操作顺序（清洗 tasks 明文口令）

先做凭据密钥迁移，再做明文清洗：前者让 `vm_credentials` 与 JWT 密钥解耦，后者清掉
历史 payload 里的明文。两者都不可逆，**每一步之前都要备份对应的表**。

```bash
# 0. 停服 / 暂停任务 worker，避免与脚本并发改写同一行
# 1. 备份（备份文件含明文口令，按涉密件保管）
mysqldump -h <DB_HOST> -u <DB_USER> -p --single-transaction \
  --set-gtid-purged=OFF --default-character-set=utf8mb4 vmops vm_credentials \
  > vm_credentials-backup-$(date +%F).sql
mysqldump -h <DB_HOST> -u <DB_USER> -p --single-transaction \
  --set-gtid-purged=OFF --default-character-set=utf8mb4 vmops tasks \
  > tasks-backup-$(date +%F).sql
chmod 600 *-backup-*.sql

# 2. 凭据密钥迁移（只读体检 → 小批量 → 全量）
CREDENTIAL_MASTER_KEY=<新密钥> go run ./scripts/credential-rekey
CREDENTIAL_MASTER_KEY=<新密钥> go run ./scripts/credential-rekey --apply

# 3. 明文清洗（只读体检 → 小批量试写 → 全量 → 复查）
go run ./scripts/purge-task-secrets
go run ./scripts/purge-task-secrets --apply --limit 50
go run ./scripts/purge-task-secrets --apply       # 中断后用 --after <最后成功ID> 续跑
go run ./scripts/purge-task-secrets               # 复查，命中数应为 0

# 4. 拉起服务，验证应用安装走 credential_id 通道正常
# 5. 确认无误后按保密流程销毁备份（备份里是明文口令）
```

## 通用注意

- 所有脚本默认 dry-run，写库必须显式 `--apply`。
- 指令执行中断时日志会打印「已成功 N 条 / 最后成功 id」，从该 ID 续跑即可，不会留下半改状态。
- 脚本只写自己负责的表；`vm_credentials` 的密文与盐不在 `purge-task-secrets` 的处理范围内。
