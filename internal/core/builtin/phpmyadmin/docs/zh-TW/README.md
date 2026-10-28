# phpMyAdmin

phpMyAdmin 透過 DATABASE_NETWORK 連線 PMA_HOST 和 PMA_PORT。在 80 連接埠登入頁填寫既有 MySQL 憑證。不啟用自動登入或任意伺服器選擇。工作階段：/sessions。

選擇已啟用管理的 Docker Compose Runtime。這些有狀態範本阻擋 Swarm 安裝。

## 參數

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

選擇固定市場版本，閱讀參數並填寫自己的機密。密碼沒有可用預設值，至少使用 16 位元組。不要將真實機密提交到市場。

[安裝與維護](operations.md)
