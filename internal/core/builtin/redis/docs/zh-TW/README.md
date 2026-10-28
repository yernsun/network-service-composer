# Redis

Redis 快取啟用 AOF 持久化；連線 6379 連接埠需要驗證。資料：/data。

選擇已啟用管理的 Docker Compose Runtime。這些有狀態範本阻擋 Swarm 安裝。

## 參數

- `REDIS_PASSWORD`

選擇固定市場版本，閱讀參數並填寫自己的機密。密碼沒有可用預設值，至少使用 16 位元組。不要將真實機密提交到市場。

[安裝與維護](operations.md)
