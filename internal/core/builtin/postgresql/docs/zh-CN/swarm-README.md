# PostgreSQL

PostgreSQL 18 使用 5432 端口。挂载父数据目录 /var/lib/postgresql，不使用旧版 /var/lib/postgresql/data 路径。

此版本支持 Docker Compose 和 Swarm Service。Swarm 使用单副本、固定数据节点、本地命名卷和先停止再启动的更新方式。数据节点离线时不会调度到其他节点。

## 参数

- `POSTGRES_PASSWORD`
- `POSTGRES_USER`
- `POSTGRES_DB`

选择 Ready/Active 数据节点。持久化服务使用单副本并固定在此节点；节点离线时服务暂停。

[Compose 与 Swarm 部署](swarm-operations.md)
