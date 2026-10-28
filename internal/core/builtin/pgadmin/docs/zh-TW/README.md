# pgAdmin

pgAdmin 在 80 連接埠使用管理員信箱和密碼登入。DATABASE_NETWORK 指定既有 PostgreSQL 網路。登入後手動註冊資料庫伺服器；pgAdmin 不建立 PostgreSQL 服務。資料：/var/lib/pgadmin。

選擇已啟用管理的 Docker Compose Runtime。這些有狀態範本阻擋 Swarm 安裝。

## 參數

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

選擇固定市場版本，閱讀參數並填寫自己的機密。密碼沒有可用預設值，至少使用 16 位元組。不要將真實機密提交到市場。

[安裝與維護](operations.md)
