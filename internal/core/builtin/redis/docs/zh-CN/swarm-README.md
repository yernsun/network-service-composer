# Redis

Redis 缓存启用 AOF 持久化；连接 6379 端口需要认证。数据：/data。

此版本支持 Docker Compose 和 Swarm Service。Swarm 使用单副本、固定数据节点、本地命名卷和先停止再启动的更新方式。数据节点离线时不会调度到其他节点。

## 参数

- `REDIS_PASSWORD`

选择 Ready/Active 数据节点。持久化服务使用单副本并固定在此节点；节点离线时服务暂停。

[Compose 与 Swarm 部署](swarm-operations.md)
