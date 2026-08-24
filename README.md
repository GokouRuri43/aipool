# AIPool（纯毛坯房，未经过任何测试）

AIPool 是一个“好友共享 AI 推理算力”原型。模型由请求端用户在本地拥有和管理；算力提供端只安装 AIPool Provider，不需要手动下载模型、安装 llama.cpp、配置 CUDA 应用或选择模型。

```text
单节点路径：本地 GGUF -> Proxy ==按需缓存==> 一个 Provider -> GPU/CPU
分布式路径：本地 GGUF -> 协调 llama-server -> 本机/朋友 1/朋友 2 的 RPC Worker
AI 应用连接请求端 localhost OpenAI API；Control 负责节点调度和短期租约。
```

第一次在某台朋友电脑使用某个模型时，Proxy 会按块传输模型。Provider 校验 SHA-256 后放入由 AIPool 管理的缓存，并自动启动软件内置的 llama.cpp；后续请求按内容摘要复用缓存。朋友无需接触模型配置，但模型权重在执行期间必须存在于远端内存或受管缓存中，否则远端 GPU 无法计算。

## 当前能力

- 一个用户池可同时接入本机、朋友 1、朋友 2 等多个独立 Provider
- 多个并发推理请求按负载、并发容量和空闲显存分散到不同节点
- 支持指定节点，或限定只用本机/只用朋友节点；响应返回实际执行节点
- 单次真实 GGUF 推理可原子预留至少两台节点，通过 llama.cpp RPC 按层共同执行
- 分布式模式下模型文件仍保存在用户电脑，Provider 只安装软件；分配的权重张量会在执行期间进入 Provider 内存

- 请求端本地 GGUF 目录和 `模型名=路径` 映射
- 本地 OpenAI 兼容 `GET /v1/models`、`POST /v1/chat/completions`
- 普通 JSON 和 SSE 流式输出
- SHA-256 内容寻址、8 MiB 分块、断点续传和完整性校验
- Host 注册、请求端访问、租约签名三类独立密钥
- 租约绑定节点、模型名称、模型摘要、大小、过期时间和随机数
- Provider 自动检测 NVIDIA GPU/CPU，按显存要求调度
- Provider 自动管理 llama.cpp 生命周期和模型切换
- 局域网地址自动发现、Host 连通性预检和有界网络超时

## 开发演示

要求 Go 1.26 或更高版本：

```powershell
.\scripts\build.cmd
.\scripts\demo.cmd
```

这条命令运行无需模型的兼容 Mock 流程。真实模型双机流程见 [docs/LAN_TESTING.md](docs/LAN_TESTING.md)。

不同网络测试使用 AIPool 自建 TLS Relay 和端到端加密，无需公网 IP、端口映射或第三方在线服务，见 [docs/INTERNET_TESTING.md](docs/INTERNET_TESTING.md)。

单次推理跨多台电脑已接入真实 llama.cpp RPC：现有多节点原子组队、真实 GGUF 逐层容量规划、RPC 加密隧道、组续租和模型常驻复用。自研 Stage/TensorWire 仍是未来降低公网延迟和精确控制层区间的实验路径；准确边界见 [docs/DISTRIBUTED_INFERENCE.md](docs/DISTRIBUTED_INFERENCE.md)。

## Windows 发布包

开发者先准备一次 Provider Runtime（只下载推理引擎，不下载模型），再生成两个 ZIP：

```powershell
.\scripts\install-provider-runtime.cmd
.\scripts\package-windows.cmd
```

产物：

- `dist/AIPool-Provider-Windows.zip`：发给朋友，内含 Host、Tunnel 和托管 Runtime。
- `dist/AIPool-Requester-Windows.zip`：用户端，内含 Control、Proxy、Tunnel 和测试工具。

朋友的正常使用流程不需要运行 Runtime 安装脚本；它只需解压 Provider ZIP 并运行 `start-provider-lan.cmd`。

生成 CPU 包时运行 `install-provider-runtime.cmd -Variant CPU`；NVIDIA Provider 包使用 `-Variant CUDA`。Runtime 下载支持断点续传。

## 双机最短流程

朋友电脑：

```powershell
.\start-provider-lan.cmd
```

用户电脑：

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://朋友局域网IP:8080' `
  -ModelDir 'D:\Models'
```

也可以显式指定公开名称与本地文件：

```powershell
.\start-requester-lan.cmd `
  -ControlURL 'http://朋友局域网IP:8080' `
  -Models 'qwen-local=D:\Models\qwen.gguf'
```

AI 应用连接：

```text
http://127.0.0.1:11434/v1
```

## 不同网络最短流程

先在你控制的公网服务器部署 `relay.exe`。用户电脑：

```powershell
.\start-requester-internet.cmd `
  -RelayAddress 'relay.example.com:8443' `
  -RelayServerName 'relay.example.com' `
  -RelayFingerprint '<SHA-256 fingerprint>' `
  -Mock
```

把生成的 Pair 文件发给朋友。朋友电脑：

```powershell
.\start-provider-internet.cmd -PairFile '.\AIPool-Pair-USER.json' -Mock
```

用户运行 `.\test-internet.cmd -Model 'mock-llm'`。Mock 通过后双方去掉 `-Mock`，用户通过 `-ModelDir` 或 `-Models` 选择本地 GGUF。加入 `-Distributed -LocalProvider` 后，请求端本机和朋友节点可加入同一个分布式组；使用至少两个朋友节点时可以省略 `-LocalProvider`。

客户端可用请求头指定最低空闲显存：

```text
X-AIPool-Min-VRAM-MB: 4096
```

## 主要环境变量

| 变量 | 端 | 用途 |
|---|---|---|
| `AIPOOL_LOCAL_MODEL_DIR` | 请求端 | 扫描其中的 `*.gguf`，文件名作为模型 ID |
| `AIPOOL_LOCAL_MODELS` | 请求端 | 逗号分隔的 `模型ID=本地路径` 映射 |
| `AIPOOL_CONTROL_URL` | 两端 | Control 地址 |
| `AIPOOL_CLIENT_SECRET` | 请求端/Control | Proxy 访问 Control |
| `AIPOOL_HOST_SECRET` | Provider/Control | Host 注册 Control |
| `AIPOOL_LEASE_SECRET` | Provider/Control | 签发和验证模型绑定租约 |
| `AIPOOL_MODEL_CACHE_DIR` | Provider | AIPool 管理的远端模型缓存 |
| `AIPOOL_MANAGED_RUNTIME` | Provider | 软件内置的 `llama-server.exe` 路径 |
| `AIPOOL_HOST_ENDPOINT` | Provider | 请求端可达的 Host 地址；默认自动发现 |

## 安全边界

局域网路径仍属于可信网络原型，不应直接暴露端口到公网。不同网络路径使用自建 TLS Relay、证书指纹固定和 AES-256-GCM 端到端加密，但尚没有朋友端批准界面、账户配额、设备吊销和 P2P QUIC。

模型“由用户本地管理”不等于远端内存永远看不到权重。单节点模式会在 Provider 保存按摘要命名的受管缓存；分布式 RPC 模式不要求朋友保存完整 GGUF，但执行必须把分配到该节点的张量放入其内存。
