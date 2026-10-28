# pgAdmin

pgAdmin 在 80 端口使用管理员邮箱和密码登录。DATABASE_NETWORK 指定已有 PostgreSQL 网络。登录后手动注册数据库服务器；pgAdmin 不创建 PostgreSQL 服务。数据：/var/lib/pgadmin。

选择已启用管理的 Docker Compose Runtime。这些有状态模板阻断 Swarm 安装。

## 参数

- `PGADMIN_DEFAULT_EMAIL`
- `PGADMIN_DEFAULT_PASSWORD`
- `DATABASE_NETWORK`

选择固定市场版本，阅读参数并填写自己的秘密。密码没有可用默认值，至少使用 16 字节。不要把真实秘密提交到市场。

[安装与维护](operations.md)
