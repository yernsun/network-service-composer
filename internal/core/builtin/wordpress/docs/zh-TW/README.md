# WordPress

WordPress 包含獨立 MySQL 服務。資料庫就緒後再存取 80 連接埠的安裝精靈。保留 database-data 與 wordpress-data 兩個卷。

選擇已啟用管理的 Docker Compose Runtime。這些有狀態範本阻擋 Swarm 安裝。

## 參數

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

選擇固定市場版本，閱讀參數並填寫自己的機密。密碼沒有可用預設值，至少使用 16 位元組。不要將真實機密提交到市場。

[安裝與維護](operations.md)
