# WordPress

WordPress 包含獨立 MySQL 服務。資料庫就緒後再存取 80 連接埠的安裝精靈。保留 database-data 與 wordpress-data 兩個卷。

此版本支援 Docker Compose 與 Swarm Service。Swarm 使用單一副本、固定資料節點、本機具名磁碟區及先停止再啟動的更新方式。資料節點離線時不會排程至其他節點。

## 參數

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

選擇 Ready/Active 資料節點。持久化服務使用單一副本並固定於此節點；節點離線時服務暫停。

[Compose 與 Swarm 部署](swarm-operations.md)
