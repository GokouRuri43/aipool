# AIPool 不同网络测试：自建 Relay

用户电脑是任务主机：保存模型、运行 Control 与本地 OpenAI Proxy。朋友电脑只运行 Provider。两端都主动连接你自己部署的公网 Relay，因此可以位于不同运营商、不同城市和不同 NAT 后面，不需要公网 IP、同一局域网或路由器端口映射。

```text
用户电脑                                      朋友电脑
Control <==端到端加密反向流==> 自建 Relay <==> Provider 注册
Proxy   <==端到端加密反向流==> 自建 Relay <==> Host/模型推理
协调端 <==端到端加密 RPC 流==> 自建 Relay <==> rpc-server
```

Relay 使用 TLS 1.3 防止协议探测和中间人攻击；每条数据流还使用配对密钥派生的 AES-256-GCM 端到端加密。连接鉴权采用一次性随机挑战和 Ed25519 签名，Pair Token 不会发送给 Relay。Relay 只看到连接、公钥形式的 Pair ID、目标类型和密文字节，不能读取模型、提示词、响应、AIPool 密钥或 HTTP 内容。

## 1. 部署自己的 Relay

准备一台有公网 TCP 入站能力的 Linux/Windows 服务器和域名，例如 `relay.example.com`。开放 TCP 8443。

开发测试可生成并固定自签名证书：

```powershell
.\scripts\generate-relay-cert.cmd -HostName 'relay.example.com'
```

记录输出的 `fingerprint_sha256`。客户端会固定该指纹，不依赖公共 CA。

Docker 部署：

```powershell
.\scripts\build.cmd
docker compose -f compose.relay.yaml up -d --build
```

也可直接运行：

```powershell
.\scripts\start-relay.cmd
```

Relay 已提供全局、单 IP、单 Pair 和待配对流连接上限，可通过 `AIPOOL_RELAY_MAX_CONNECTIONS`、`AIPOOL_RELAY_MAX_CONNECTIONS_PER_IP`、`AIPOOL_RELAY_MAX_CONNECTIONS_PER_PAIR` 和 `AIPOOL_RELAY_MAX_PENDING_PER_PAIR` 调整。生产环境还应将 Relay 放在受限普通用户下，配置系统服务、日志轮转、带宽配额和监控。

## 2. 用户电脑启动任务主机

当前请求端以“个人算力池”方式管理多个 Provider。第一次为朋友 1 生成配对：

先运行 Mock，验证不同网络连接：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<fingerprint_sha256>' `
	-ProviderName 'friend-1' `
  -Mock
```

增加朋友 2 时先生成其独立 Pair 文件：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<fingerprint_sha256>' `
  -ProviderName 'friend-2' `
  -AddProvider
```

每个朋友只收到自己的 Pair 文件。重新启动请求端后，它会为池中所有朋友建立独立隧道。需要把用户自己的电脑也纳入池时添加 `-LocalProvider`；Requester 包因此同时包含 `host.exe`。

脚本在用户桌面生成 `AIPool-Pair-<ProviderName>.json`。通过可信渠道发给对应朋友。每份配对文件包含该 Provider 独有的高熵能力令牌、端到端加密密钥、注册密钥和租约校验密钥，但不包含用户 Proxy 的客户端密钥。持有者可以作为该节点连接用户，应像密码一样保护。

真实本地模型：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<fingerprint_sha256>' `
  -Models 'qwen-local=D:\Models\qwen.gguf'
```

单次推理同时使用“本机 + 朋友 1”（至少两台节点）：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<fingerprint_sha256>' `
  -Models 'qwen-local=D:\Models\qwen.gguf' `
  -Distributed `
  -LocalProvider `
  -DistributedMinNodes 2 `
  -DistributedMaxNodes 2
```

若有两个朋友 Provider 在线，可以省略 `-LocalProvider`。如旧 Pair 文件被启动脚本补入 RPC 端口，请把提示列出的更新后 Pair 文件重新发给对应朋友再测试分布式模式。

用户侧服务只监听本机：

- Control：`127.0.0.1:28080`
- Provider 映射：`127.0.0.1:28091`
- OpenAI API：`127.0.0.1:11434/v1`

## 3. 朋友电脑启动 Provider

Mock：

```powershell
.\start-provider-internet.cmd -PairFile '.\AIPool-Pair-USER.json' -Mock
```

真实 GPU/CPU 推理：

```powershell
.\start-provider-internet.cmd -PairFile '.\AIPool-Pair-USER.json'
```

Provider 的 Host 只监听 `127.0.0.1:28091`。它通过出站隧道访问用户电脑的 Control，不在朋友的物理网卡或公网开放任何 AIPool 端口。
真实模式还会启动只监听 `127.0.0.1:50052` 的 `rpc-server`，并通过端到端加密隧道映射到用户电脑；不要在路由器或防火墙中公开该端口。

## 4. 验证

用户电脑另开终端：

```powershell
.\test-internet.cmd -Model 'mock-llm'
```

在两个或更多 Provider 在线时测试并发分发：

```powershell
.\test-internet.cmd -Model 'mock-llm' -Parallel 3
```

Proxy 会按 `(Host 已报告任务数或 Control 已保留槽位) / 最大并发数` 选择负载最低节点，并以空闲显存作为次级排序。同一时间的多个独立请求可分别使用本机、朋友 1、朋友 2；这不是将单次推理拆分到多台机器。

可选请求头：

- `X-AIPool-Node-ID: friend-1`：指定节点。
- `X-AIPool-Scope: local`：只使用本机 Provider。
- `X-AIPool-Scope: remote`：只使用朋友 Provider。

响应头 `X-AIPool-Node-ID` 表示实际执行节点。

真实模型改成对应的本地模型 ID。测试包含非流式和 SSE 流式响应。

单次分布式推理：

```powershell
.\test-internet.cmd -Model 'qwen-local' -Execution distributed
```

成功响应带 `X-AIPool-Execution: distributed` 与 `X-AIPool-Nodes`。首次请求需把所分配的张量从用户电脑传到多个 Worker，耗时可能较长；模型常驻后续请求会复用该组。当前公网 RPC 热路径全部经过 Relay，功能正确性优先于性能。

## 当前连接方式

当前版本所有不同网络流量经过自建 Relay，但内容端到端加密。这样能先保证任何 NAT 环境都可工作。下一阶段加入自建协调服务、STUN 和 QUIC 打洞：优先让两台用户电脑点对点直连，失败时再复用当前 Relay，降低大模型传输带宽成本。

## 安全与运营边界

- 不依赖 Tailscale、Headscale 或其他第三方公司的在线服务。
- Relay TLS 证书通过 SHA-256 指纹固定。
- 配对文件泄露后应删除旧文件并生成新配对，未来会加入设备吊销服务。
- 当前 Relay 鉴权是无数据库的挑战—响应能力凭证，适合原型；大规模运营仍需增加 AIPool 账户、设备身份、短期凭证、吊销、限速和滥用防护。
- Relay 可看到 Pair ID、连接时间、字节数和目标类别等元数据，但看不到数据内容。
- 远端模型缓存当前仍为明文；后续加入缓存加密、容量配额和自动删除策略。
