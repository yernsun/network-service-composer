# pgAdmin

pgAdmin 在 80 端口使用管理员邮箱和密码登录。DATABASE_NETWORK 指定已有 PostgreSQL 网络。登录后手动注册数据库服务器；pgAdmin 不创建 PostgreSQL 服务。数据：/var/lib/pgadmin。

此版本支持 Docker Compose 和 Swarm Service。Swarm 使用单副本、固定数据节点、本地命名卷和先停止再启动的更新方式。数据节点离线时不会调度到其他节点。

## 参数

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

选择 Ready/Active 数据节点。持久化服务使用单副本并固定在此节点；节点离线时服务暂停。

[Compose 与 Swarm 部署](swarm-operations.md)
