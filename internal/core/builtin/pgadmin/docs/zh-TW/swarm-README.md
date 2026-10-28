# pgAdmin

pgAdmin 在 80 連接埠使用管理員信箱和密碼登入。DATABASE_NETWORK 指定既有 PostgreSQL 網路。登入後手動註冊資料庫伺服器；pgAdmin 不建立 PostgreSQL 服務。資料：/var/lib/pgadmin。

此版本支援 Docker Compose 與 Swarm Service。Swarm 使用單一副本、固定資料節點、本機具名磁碟區及先停止再啟動的更新方式。資料節點離線時不會排程至其他節點。

## 參數

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

選擇 Ready/Active 資料節點。持久化服務使用單一副本並固定於此節點；節點離線時服務暫停。

[Compose 與 Swarm 部署](swarm-operations.md)
