# Redis

Redis 缓存启用 AOF 持久化；连接 6379 端口需要认证。数据：/data。

选择已启用管理的 Docker Compose Runtime。这些有状态模板阻断 Swarm 安装。

## 参数

- `REDIS_PASSWORD`

选择固定市场版本，阅读参数并填写自己的秘密。密码没有可用默认值，至少使用 16 字节。不要把真实秘密提交到市场。

[安装与维护](operations.md)
