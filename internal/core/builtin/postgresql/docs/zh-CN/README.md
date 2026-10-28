# PostgreSQL

PostgreSQL 18 使用 5432 端口。挂载父数据目录 /var/lib/postgresql，不使用旧版 /var/lib/postgresql/data 路径。

选择已启用管理的 Docker Compose Runtime。这些有状态模板阻断 Swarm 安装。

## 参数

- `POSTGRES_PASSWORD`
- `POSTGRES_USER` = `app`
- `POSTGRES_DB` = `app`

选择固定市场版本，阅读参数并填写自己的秘密。密码没有可用默认值，至少使用 16 字节。不要把真实秘密提交到市场。

[安装与维护](operations.md)
