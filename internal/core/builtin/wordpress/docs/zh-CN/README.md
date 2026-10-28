# WordPress

WordPress 包含独立 MySQL 服务。数据库就绪后再访问 80 端口的安装向导。保留 database-data 和 wordpress-data 两个卷。

选择已启用管理的 Docker Compose Runtime。这些有状态模板阻断 Swarm 安装。

## 参数

- `MYSQL_ROOT_PASSWORD`
- `MYSQL_PASSWORD`
- `MYSQL_DATABASE` = `app`
- `MYSQL_USER` = `app`

选择固定市场版本，阅读参数并填写自己的秘密。密码没有可用默认值，至少使用 16 字节。不要把真实秘密提交到市场。

[安装与维护](operations.md)
