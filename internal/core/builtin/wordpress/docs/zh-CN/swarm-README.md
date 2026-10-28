# WordPress

WordPress 包含独立 MySQL 服务。数据库就绪后再访问 80 端口的安装向导。保留 database-data 和 wordpress-data 两个卷。

此版本支持 Docker Compose 和 Swarm Service。Swarm 使用单副本、固定数据节点、本地命名卷和先停止再启动的更新方式。数据节点离线时不会调度到其他节点。

## 参数

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE`
- `MYSQL_USER`

选择 Ready/Active 数据节点。持久化服务使用单副本并固定在此节点；节点离线时服务暂停。

[Compose 与 Swarm 部署](swarm-operations.md)
