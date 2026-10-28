# phpMyAdmin

phpMyAdmin 通过 DATABASE_NETWORK 连接 PMA_HOST 和 PMA_PORT。在 80 端口登录页填写已有 MySQL 凭据。不启用自动登录或任意服务器选择。会话：/sessions。

选择已启用管理的 Docker Compose Runtime。这些有状态模板阻断 Swarm 安装。

## 参数

- `PMA_HOST`
- `PMA_PORT` = `3306`
- `DATABASE_NETWORK`

选择固定市场版本，阅读参数并填写自己的秘密。密码没有可用默认值，至少使用 16 字节。不要把真实秘密提交到市场。

[安装与维护](operations.md)
