# phpMyAdmin

phpMyAdmin 通过 DATABASE_NETWORK 连接 PMA_HOST 和 PMA_PORT。在 80 端口登录页填写已有 MySQL 凭据。不启用自动登录或任意服务器选择。会话：/sessions。

此版本支持 Docker Compose 和 Swarm Service。Swarm 使用单副本、固定数据节点、本地命名卷和先停止再启动的更新方式。数据节点离线时不会调度到其他节点。

## 参数

- `PMA_HOST`
- `PMA_PORT`
- `DATABASE_NETWORK`

选择 Ready/Active 数据节点。持久化服务使用单副本并固定在此节点；节点离线时服务暂停。

[Compose 与 Swarm 部署](swarm-operations.md)
