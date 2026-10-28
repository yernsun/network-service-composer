# PostgreSQL

PostgreSQL 18 使用 5432 連接埠。掛載父資料目錄 /var/lib/postgresql，不使用舊版 /var/lib/postgresql/data 路徑。

此版本支援 Docker Compose 與 Swarm Service。Swarm 使用單一副本、固定資料節點、本機具名磁碟區及先停止再啟動的更新方式。資料節點離線時不會排程至其他節點。

## 參數

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

選擇 Ready/Active 資料節點。持久化服務使用單一副本並固定於此節點；節點離線時服務暫停。

[Compose 與 Swarm 部署](swarm-operations.md)
