# PostgreSQL

PostgreSQL 18 使用 5432 連接埠。掛載父資料目錄 /var/lib/postgresql，不使用舊版 /var/lib/postgresql/data 路徑。

選擇已啟用管理的 Docker Compose Runtime。這些有狀態範本阻擋 Swarm 安裝。

## 參數

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

選擇固定市場版本，閱讀參數並填寫自己的機密。密碼沒有可用預設值，至少使用 16 位元組。不要將真實機密提交到市場。

[安裝與維護](operations.md)
