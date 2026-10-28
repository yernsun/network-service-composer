# WordPress

支持 Docker Compose；此模板不支持 Swarm。使用本地命名卷，不提供跨节点数据复制或业务数据回滚。

## 安装与访问

包含独立 MySQL，先等待数据库健康再启动 WordPress。仅给 wordpress:80 绑定入口，不给 database 绑定 HTTP Route。首次访问完成站点管理员初始化；数据库密码不是站点登录密码。保留 wordpress-data（主题/插件/上传）和 database-data 两个卷。官方镜像识别受信任入口的 X-Forwarded-Proto。

默认不发布任何宿主机端口。Web 服务安装成功后，在 NSC 为其绑定自己的 HTTPS 域名（推荐域名只是占位示例，不会自动创建）。数据库协议不能作为 HTTP Route 发布。

变量中的密码由用户提供，至少 16 字节，无默认口令；普通预览会脱敏。数据库初始化变量仅对空数据卷生效；修改安装变量不等于修改现存数据库用户密码。

## 持久化与退出

先备份命名卷及应用自身的逻辑数据，再升级。停止/重启保留数据；删除应用默认不删除卷。重用旧卷需核对数据库主版本及原账号；镜像/配置恢复不会恢复卷内数据。

## 来源

- https://hub.docker.com/_/wordpress
- https://hub.docker.com/_/mysql
