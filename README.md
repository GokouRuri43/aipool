# AIPool（纯毛坯房，未经过任何测试）

<p align="center">
  <strong>由你掌控的个人 AI 算力池</strong><br>
  让自己的电脑与获得许可的朋友电脑共同承担本地大模型推理。
</p>

<p align="center">
  <a href="README_EN.md"><img alt="English README" src="https://img.shields.io/badge/README-English-0969da"></a>
</p>

> [!IMPORTANT]
> AIPool 目前是供开发与测试使用的早期原型，不是可直接投入生产的算力平台。真实跨公网、多硬件组合和故障恢复仍需要人工验证。

## AIPool 是什么

AIPool 面向这样的使用方式：模型文件由请求者保存在自己的电脑上，朋友只需安装并运行 AIPool Provider，无需自己寻找模型、配置 llama.cpp 或操作 CUDA。请求者的 AI 应用连接本机 OpenAI 兼容 API，由 AIPool 选择一台节点执行任务，或把一次推理拆到多台节点共同执行。

```text
AI 应用
   │  OpenAI 兼容 API（仅本机）
   ▼
Requester：Proxy + Control + 本地 GGUF
   │
   ├── 单节点推理 ───────────────► 一台 Provider（本机或朋友电脑）
   │
   └── 单次分布式推理 ──────────► 本机 + 朋友 1 + 朋友 2 ...
                 llama.cpp RPC       按可用内存/显存分配权重张量
```

设备在同一局域网时可以直接连接；位于不同网络时，双方主动连接你自己部署的 AIPool Relay。跨网方案不依赖 Tailscale、Headscale 或其他第三方公司的在线服务。

## 模型究竟在哪里

- 模型源文件和模型目录由请求者在本地选择、命名和管理。
- 单节点模式首次使用某个模型时，会以 8 MiB 分块传输完整 GGUF。Provider 校验 SHA-256 后保存到 AIPool 受管缓存，后续请求复用缓存。
- 分布式模式由请求者的协调 `llama-server` 读取本地 GGUF，并通过 llama.cpp RPC 把分配到各节点的权重张量传入内存。朋友端不需要持有或手动管理完整 GGUF 文件。
- 任何远端计算都不可能让远端设备“完全看不到”参与计算的数据。当前单节点缓存还是明文，因此只应使用你信任且明确授权的电脑。

## 当前能力

### 个人多节点算力池

- 一个请求者可登记本机以及多个朋友 Provider。
- 多个独立推理请求可按负载、并发容量和空闲显存分散到不同节点。
- 请求可指定节点，或限定只使用本机/朋友节点；响应头会返回实际执行节点。
- Provider 自动检测 NVIDIA GPU、显存、CPU 和内存，并向 Control 报告状态。

### 单次推理跨多台电脑

- 为一次推理原子预留至少两台节点，任一节点容量不足时整组预留失败。
- 解析 GGUF v2/v3 元数据，根据真实逐层张量大小制定连续层容量计划。
- 当前真实执行后端使用 llama.cpp RPC、`--split-mode layer` 和 `--tensor-split`。
- RPC Worker 仅监听 Provider 本机地址，通过 AIPool 加密隧道映射到请求者。
- 分布式会话支持组续租、持久连接和模型常驻复用。
- 自研 Stage/TensorWire 已实现带 HMAC 的有序二进制激活帧和连续层实验流水线，但目前仍是协议验证后端，不是正式的 GGUF 推理后端。

### API、模型传输与运行时

- 本机 OpenAI 兼容 `GET /v1/models` 和 `POST /v1/chat/completions`。
- 支持普通 JSON 与 SSE 流式响应。
- 支持本地 GGUF 目录扫描和 `模型ID=本地路径` 映射。
- 模型传输采用 SHA-256 内容寻址、断点续传和完整性校验。
- Provider 自动启动、切换和停止随发布包提供的 llama.cpp Runtime。
- Host 注册、Requester 访问和租约签名使用相互独立的密钥。

### 不同网络连接

- 自建 Relay 支持 TLS 1.3、证书 SHA-256 指纹固定。
- 流量使用配对密钥派生的 AES-256-GCM 端到端加密。
- Relay 鉴权使用随机挑战与 Ed25519 签名，Pair Token 不发送给 Relay。
- 两端只需建立出站连接，不需要处于同一局域网，也不需要公网 IP 或路由器端口映射。
- Relay 只能看到连接元数据和密文字节，不能读取模型、提示词、响应或 AIPool 内部密钥。

## 当前开发进度

| 模块 | 状态 | 当前边界 |
|---|---|---|
| Control、Host、Proxy 与 OpenAI 兼容接口 | 已实现并通过自动化测试 | 仍需完善 API 兼容范围和错误协议 |
| 单 Provider 的 Mock 推理 | 已验证 | 用于快速检查完整链路，不代表真实模型性能 |
| 单 Provider 的真实 GGUF 推理 | 已实现 | 等待不同 CPU/GPU 和双物理机人工测试 |
| 多 Provider 注册与并发请求调度 | 已实现并有单元/集成测试 | 等待真实多机负载与节点掉线测试 |
| 单次推理拆分到多节点 | llama.cpp RPC 路径已接入 | 本地 Smoke 脚本已就绪；异地真实硬件性能与稳定性待测 |
| 自研 Stage/TensorWire | 协议和确定性流水线已实现 | 尚未接入完整 Transformer/GGUF 算子 |
| 自建跨网 Relay | 加密传输与鉴权已实现并有 E2E 测试 | 真实公网 NAT、长时间运行和弱网恢复待测 |
| Windows Provider/Requester 发布包 | 构建、下载 Runtime 和校验脚本已实现 | 尚无图形安装器、自动更新和代码签名 |
| 生产级账户、授权、配额和运维 | 尚未实现 | 当前 Pair 文件属于高权限凭证，只适合受信任测试 |

目前已经完成代码层面的原型与自动化验证。下一阶段需要人工准备至少两台物理电脑和一台自建公网 Relay，验证真实 GGUF、异构硬件、跨 NAT、断线重连、吞吐与首 Token 延迟。

## 快速体验：无模型 Mock

开发环境要求 Windows PowerShell 和 Go 1.26 或更高版本：

```powershell
git clone https://github.com/GokouRuri43/aipool.git
cd aipool
.\scripts\build.cmd
.\scripts\demo.cmd
```

该命令会在本机启动 Control、Mock Host 和 Proxy，发送一次兼容 OpenAI 格式的请求，然后自动停止进程。它不会下载或运行模型。

## 构建 Windows 测试包

开发者先下载一次 llama.cpp Runtime，再生成 Provider 和 Requester 两个 ZIP：

```powershell
.\scripts\install-provider-runtime.cmd -Variant CUDA
.\scripts\package-windows.cmd
```

没有 NVIDIA GPU 时可使用 `-Variant CPU`。生成结果：

- `dist/AIPool-Provider-Windows.zip`：交给朋友，包含 Host、Tunnel、Stage 和托管 Runtime，不包含模型。
- `dist/AIPool-Requester-Windows.zip`：请求者使用，包含 Control、Proxy、Tunnel、Host 和测试工具。

朋友不需要运行 Runtime 安装脚本；解压完整 Provider ZIP 后直接运行启动脚本。

## 局域网最短测试

朋友电脑：

```powershell
.\start-provider-lan.cmd `
  -HostSecret '<host-secret>' `
  -ClientSecret '<client-secret>' `
  -LeaseSecret '<lease-secret>'
```

请求者电脑：

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://朋友局域网IP:8080' `
  -ClientSecret '<client-secret>' `
  -Models 'qwen-local=D:\Models\model.gguf'
```

然后运行：

```powershell
.\test-lan.cmd -Model 'qwen-local'
```

完整步骤、防火墙规则和排错方法见 [局域网测试指南](docs/LAN_TESTING.md)。局域网端口只应对专用网络和本地子网开放，不得映射到公网。

## 不同网络最短测试

先在你控制的公网服务器部署 `relay.exe`，开放 TCP 8443，并生成或配置 TLS 证书。请求者运行：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<SHA-256 fingerprint>' `
  -ProviderName 'friend-1' `
  -Mock
```

脚本会在桌面生成该 Provider 独有的 `AIPool-Pair-friend-1.json`。通过可信渠道把它交给对应朋友，朋友运行：

```powershell
.\start-provider-internet.cmd -PairFile '.\AIPool-Pair-friend-1.json' -Mock
```

请求者另开终端验证：

```powershell
.\test-internet.cmd -Model 'mock-llm'
```

Mock 通过后，双方去掉 `-Mock`，请求者用 `-ModelDir` 或 `-Models` 指定本地 GGUF。增加朋友使用 `-ProviderName 'friend-2' -AddProvider`。启用单次多节点推理时加入：

```powershell
-Distributed -LocalProvider -DistributedMinNodes 2 -DistributedMaxNodes 2
```

若已有至少两个朋友 Provider，可以省略 `-LocalProvider`。完整 Relay 部署、配对和测试流程见 [不同网络测试指南](docs/INTERNET_TESTING.md)。

## 客户端调用

AI 应用使用本机地址：

```text
http://127.0.0.1:11434/v1
```

可选请求头：

| 请求头 | 用途 |
|---|---|
| `X-AIPool-Node-ID: friend-1` | 指定执行节点 |
| `X-AIPool-Scope: local` | 只允许本机 Provider |
| `X-AIPool-Scope: remote` | 只允许朋友 Provider |
| `X-AIPool-Min-VRAM-MB: 4096` | 要求最低空闲显存 |
| `X-AIPool-Execution: distributed` | 请求单次多节点推理 |

响应头 `X-AIPool-Node-ID` 返回单节点执行者；分布式响应使用 `X-AIPool-Execution: distributed` 和 `X-AIPool-Nodes`。

## 安全边界

- 只连接你信任并明确授权的设备，不要在不知情的电脑上部署 Provider。
- Pair 文件包含高权限能力凭证和端到端加密密钥，应像密码一样保存并通过可信渠道传递。
- 当前还没有账户系统、朋友端批准界面、设备吊销、用量配额、沙箱隔离、缓存加密和自动清理。
- Provider 会处理模型权重和用户请求；AIPool 不能对已被控制的 Provider 主机提供机密性保证。
- 跨网 Relay 内容虽已端到端加密，但 Relay 仍能观察 Pair ID、连接时间、目标类别和流量大小等元数据。
- 当前公网热路径全部经过 Relay，优先保证可连接与协议正确；尚未实现 P2P QUIC、激活压缩和生产级弱网优化。

## 已知限制与路线图

- 当前只支持单文件 GGUF；尚不支持 safetensors、多文件权重或训练任务。
- 增加真实双机/多机、不同显卡、CPU-only 和跨公网回归测试。
- 优化首轮权重传输、连接复用、预热、KV Cache 与模型组生命周期。
- 加入 P2P QUIC 直连，失败时回退到自建 Relay。
- 加入 Provider 可视化批准、资源上限、温度/功耗策略、暂停和一键撤销。
- 加入账户/设备身份、短期凭证、吊销、配额、审计和滥用防护。
- 加入缓存加密、容量淘汰、自动清理、桌面 UI、安装器、自动更新和代码签名。

分布式实现细节与准确边界见 [分布式推理架构](docs/DISTRIBUTED_INFERENCE.md)。

## 开发验证

```powershell
go test ./...
go vet ./...
.\scripts\smoke-relay.cmd
```

真实 llama.cpp 本地门禁脚本：

```powershell
.\scripts\smoke-distributed-real.cmd
.\scripts\smoke-distributed-pool-real.cmd
.\scripts\smoke-distributed-relay-real.cmd
```

这些 Smoke 测试不能替代多台物理电脑上的人工测试。
