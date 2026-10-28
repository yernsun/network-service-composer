// Detail/editor terminology. Unlisted user-authored text keeps its source spelling.
const entries = `
CPU / 内存|CPU / memory
CPU 使用|CPU usage
Compose / Stack 文件|Compose / Stack file
Ctrl / ⌘ S 保存草稿|Ctrl / ⌘ S to save draft
Docker Swarm 集群 · 通过 Manager|Docker Swarm cluster · Via manager
Docker 单机主机|Standalone Docker host
Domain 配置与扩展|Domain configuration & extensions
Endpoint 快照|Endpoint snapshot
Gateway 生效|Applied gateways
HTTP 自动跳转到 HTTPS|Automatically redirect HTTP to HTTPS
HTTP 跳转 HTTPS|Redirect HTTP to HTTPS
NSC 本地演示，非生产认证模板|Local NSC demo, not a production-certified template
OOM 证据|OOM evidence
TLS 与自定义指令|TLS & custom directives
listen 443 ssl · 关联 Domain 证书|listen 443 ssl · Associated domain certificate
location 与目标服务由 Route 管理。|Routes manage locations and target services.
一个 Domain 可以拥有多个 Route。|A domain can have multiple routes.
一个 Service 可以被多个 Domain 的 Route 复用。|Routes across domains can share a service.
一个项目是一组应用入口与配置的管理边界。可以只有一个生产环境。|A project defines the management boundary for application ingress and configuration. It can have just one production environment.
上一个 Task|Previous task
上次模拟部署的渲染结果|Rendered output of the last simulated deployment
下一次应用部署的结果|Output for the next application deployment
不会自动复制生产域名或凭据。|Production domains and credentials are not copied automatically.
不会自动拉取镜像，也不会执行你填写的内容。|Images are not pulled and the supplied content is not executed.
不填写真实密码，不复制其他环境凭据。|Do not enter real passwords or copy credentials from another environment.
不必先拥有域名，也不需要自动复制生产配置。|A domain is not required, and production configuration need not be copied.
不按同名自动绑定；候选服务仅来自目标项目 / 环境。|Names do not create automatic bindings. Candidates belong to the target project and environment.
不提供 HTTP 时填写 0。这里不是公网或宿主机端口。|Enter 0 when HTTP is not provided. This is not a public or host port.
不是将旧 Task 改回 Running|The old task is not changed back to Running
不显示虚构的响应耗时；代理结果不推断为 HTTP 200。|No response timings are invented, and proxy results do not imply HTTP 200.
不自动创建开发 / 测试环境，不复制其他项目的资源。|Development/test environments are not created automatically, and resources are not copied from other projects.
不限制根域名，不根据环境名称自动生成。|No root-domain restriction; names are not generated from the environment.
与当前期望版本一致|Matches the current desired version
业务请求是否恢复仍需独立请求探测。|Application recovery requires an independent request probe.
个匹配服务|matching services
个可见变量 ·|visible variables ·
个定义在当前层级|defined at this layer
个目标服务|target services
个环境引用|environment references
为当前域名开启 443 端口，并使用关联证书。|Enable port 443 for this domain using its associated certificate.
为每个服务明确选择目标|Select an explicit target for each service
为每条路由分别设置；不会用域名级默认值覆盖已有路由设置。|Configure each route independently. Domain defaults do not overwrite existing route settings.
为环境绑定域名|Bind a domain to an environment
事件时间线|Event timeline
事件覆盖|Event coverage
交互演示环境|Interactive demo environment
交互说明|Interaction guide
仅 Ready 目标进入候选配置|Only Ready targets enter the candidate configuration
仅一个环境 · 随时可添加|One environment · Add more at any time
仅使用本地演示数据。|Local demo data only.
仅修改当前域名草稿，发布后才生效。|Changes affect this domain draft and take effect after publishing.
仅内部运行（默认）|Internal only (default)
仅创建示例引用，不复制源环境私钥，不执行真实签发。|Creates a demo reference without copying private keys or issuing certificates.
仅支持完整 ASCII 域名；不包含协议、路径或端口。|Use a complete ASCII hostname, without protocol, path, or port.
代理读取超时|Proxy read timeout
份部署草稿。|deployment drafts.
你将直接修改当前域名的完整配置。|You will edit the complete configuration of this domain.
使用一次后自动回到成功场景；失败不清除旧 Task。|Returns to the success scenario after one use. Failures do not remove old tasks.
使用优先级|Precedence
使用率总览|Utilization overview
例如 32m。只修改当前域名，不影响其他环境。|For example, 32m. Only this domain changes; other environments are unaffected.
保存、检查和发布|Save, validate, and publish
保存不等于发布 · 下发不等于生效 · 未知不会显示为正常|Saved is not published · Sent is not applied · Unknown is not healthy
保存与发布|Save & publish
保存和发布仍然分离；不会即时修改运行配置。|Saving and publishing remain separate. Running configuration is not changed immediately.
保留当前运行目标；发布已阻断。|Current runtime targets are preserved. Publishing is blocked.
修改 API 路由|Edit API route
修改创建草稿，不直接改变运行配置。|Editing creates a draft without changing runtime configuration.
修改此层是否影响下次部署|Whether this layer affects the next deployment
修改范围|Change scope
修改边界|Change boundary
修改这项服务|Edit this service
候选配置|Candidate configuration
值 / 凭据引用|Value / credential reference
值的类型|Value type
停用会从候选配置中移除，不删除草稿定义。|Disabling removes the route from candidate configuration while preserving its draft definition.
健康 / 退出码|Health / exit code
健康检查|Health check
允许哪个环境使用|Which environments can use this target
允许环境|Allowed environments
先保存草稿，再校验与发布|Save a draft, then validate and publish
先在环境设置中选择可用网关，再返回此处绑定。|Select available gateways in environment settings, then return to bind them.
入口 Gateway|Ingress gateway
全局资源搜索|Global resource search
搜索 Domain、Service 或功能…|Search domains, services, or features…
全部为演示地址与健康状态，未进行真实网络探测。|Addresses and health states are demos; no real network probes are performed.
全部成功并核实|All successful and verified
关联|Association
关联部署|Related deployment
关闭弹层|Close overlay
兼容目标|Compatible targets
内存 MiB|Memory MiB
内存使用|Memory usage
内容范围|Content scope
内部 HTTP 端口|Internal HTTP port
内部端口|Internal port
凭据引用 · 不显示内容|Credential reference · Content hidden
切换后仍需保存草稿、校验、审阅差异并发布。|After switching, save the draft, validate, review the diff, and publish.
切换运维视角|Switch to Operations
创建时间 · UTC+8|Created · UTC+8
创建替换 Task|Create replacement task
初始目标 Service|Initial target service
初始路由目标|Initial route target
包含已退出实例|Include exited instances
匹配 URI 路径，不包含 Host、查询参数或片段。|Match the URI path, excluding host, query, and fragment.
匹配方式|Match mode
匹配类型：精确 = / 前缀 / 优先前缀 ^~|Match types: exact = / prefix / preferred prefix ^~
历史配置内容|Historical configuration
原型保留失败节点的旧版本，不会用组内其他节点的成功代替它。|Failed nodes keep their previous versions. Success on another node does not replace a failed result.
原始编排文件（完整保留，不自动覆盖）|Original Compose file (preserved, never overwritten automatically)
参数已绑定到|Parameters bound to
参数检查|Parameter checks
反向引用|References
发布前可以查看完整差异，确认受影响范围。|Review the full diff and affected scope before publishing.
发布并模拟故障|Publish and simulate failure
发布目标与限制|Release targets & limitations
发布网关|Release gateways
发现状态|Discovery state
变化原因|Change reason
变更说明|Change note
变量名 / 用途|Variable / purpose
只修改配置，不自动重启。|Changes configuration without restarting automatically.
只列出本项目、当前环境的服务。|Only services in this project and environment are listed.
只列出此环境允许使用的位置。更换位置不会搬迁已有应用。|Only permitted runtimes are listed. Changing the selection does not move existing applications.
只展示声明的覆盖层；镜像内部 ENV 默认值未读取，运行时值尚未验证。|Only declared overrides are shown. Image ENV defaults are not read and runtime values are not verified.
只想补充几条指令时，不需要切换。|No mode switch is needed to add a few directives.
只是补充几条指令时，不需要切换。|No mode switch is needed to add a few directives.
只补充几条指令？使用上方自定义指令即可，无需切换编辑方式。|To add a few directives, use custom directives above without switching authoring mode.
可以是任意合法域名，与根域名无关|Any valid domain, independent of the root domain
可导入|Import supported
可用发布网关（可稍后选择）|Available release gateways (can be selected later)
同一容器重启|Restart of the same container
同服务 / 同副本位置|Same service / replica slot
后端端口|Backend port
启动 / 结束|Started / finished
启动 / 结束时间|Start / finish time
启动文件|Startup file
启用 HTTPS|Enable HTTPS
启用此 Route|Enable this route
命中 Route|Matched route
响应状态码|Response status code
哪些域名正在引用这个服务|Domains referencing this service
回退配置不回退数据库或数据卷|Rolling back configuration does not roll back databases or volumes
固定内容与目标|Fixed content & targets
固定内容版本|Fixed content version
固定发布范围|Fixed release scope
固定应用与参数快照|Fixed application & parameter snapshot
固定目标 Gateway|Fixed target gateways
在 Cloud Native 中组织入口|Organize ingress in Cloud Native
在 Conf 工作台编辑|Edit in the configuration workspace
在本项目内唯一。不是域名前缀，也不会自动生成 DNS。|Unique within this project. It is not a domain prefix and does not generate DNS records.
在线 / 调度状态|Connection / scheduling state
在线只说明控制连接可用，不代表目标配置已经被加载。|Online means the control connection is available, not that the target configuration is loaded.
在线状态|Connection state
在线节点|Online nodes
地址|Address
域名 / 服务|Domain / service
域名与访问方式|Domain & access
域名明确绑定项目下的环境；共享网关仍需检查入口冲突。|Domains belong to a specific project environment. Shared gateways still require ingress conflict checks.
域名是显式绑定|Domains use explicit bindings
基线 v|Baseline v
填写后视为演示 Ready，不执行真实健康检查。|Treated as Ready for the demo; no real health check is performed.
声明值|Declared value
处理动作|Action
复制不是生产克隆|Copying is not a production clone
复制哪个域名的配置|Which domain configuration to copy
复制自定义指令时，我已检查其中的地址、文件路径与凭据引用。|I reviewed addresses, file paths, and credential references in the custom directives.
复制自定义指令（默认不复制，可能包含环境专属地址）|Copy custom directives (off by default; may contain environment-specific addresses)
复用内容|Reuse content
复用另一环境的配置|Reuse configuration from another environment
失联或验证结果过期时，运行版本必须显示未知。|The runtime version is unknown when disconnected or verification has expired.
失败时保留已有有效证书，不能因为续期失败删除运行资产。|Keep valid certificates on failure. Failed renewal must not delete active assets.
存在外部修改|External changes detected
安全退出与导出|Exit safely & export
完整域名|Full domain name
定义来源|Definition source
实例 / 副本位置|Instance / replica slot
实例数|Instance count
实际来源|Actual source
实际状态|Actual state
审阅来源自定义指令|Review source custom directives
容器 ID|Container ID
容器事件采集|Container event collection
容器变量|Container variables
容器环境|Container environment
容器重启计数|Container restart count
容器镜像|Container image
将创建|Will create
将创建在|Will be created in
小写英文、数字和连字符；用于唯一定位项目。|Lowercase letters, digits, and hyphens; uniquely identifies the project.
尚未绑定域名。环境可以先于域名创建。|No domains are bound yet. Environments can be created before domains.
展示结构化声明；发布结果在运维视角逐节点核实。|Shows structured declarations. Verify each node’s release outcome in Operations.
工作模型|Execution model
已保留的文件副本|Retained file copies
单环境项目|Single-environment project
多环境项目|Multi-environment project
尚未添加描述|No description yet
进入项目|Open project
项目、环境与域名均有独立标识。相同名称不会自动关联，域名的 dev / test 前缀也不会决定环境。|Projects, environments, and domains have independent identifiers. Matching names and dev/test prefixes do not establish ownership.
`.trim()
export const englishDetails: Record<string,string> = Object.fromEntries(entries.split('\n').map(line => {
  const split = line.indexOf('|')
  return [line.slice(0,split),line.slice(split+1)]
}))
