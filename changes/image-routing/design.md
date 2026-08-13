# 设计文档:image-routing 图片请求改路插件

## 相关事实与约束

**调研结论(插件可行性,已完成验证):**
- 主线已内置完整的插件 ModelRouter 管线:`cmd/server` 创建 `pluginhost.Host` → `WithPluginHost` → `BaseAPIHandler.SetPluginHost`,`modelRouterHost()` 自动回退到 `PluginHost`(实现 `PluginModelRouterHost` 接口,见 `sdk/api/handlers/handlers.go`)。插件声明 `model_router` 能力后,`applyModelRouter` 会在模型→provider 解析和凭证选择之前自动调用 `RouteModel`(`internal/pluginhost/model_router.go`)。
- 插件收到的 `ModelRouteRequest`(见 `sdk/pluginapi/types.go`):`Body`(原始客户端请求体)、`SourceFormat`(入口协议)、`RequestedModel`、`Stream`、`AvailableProviders`(当前有凭证的内置通道列表)。返回 `ModelRouteResponse`:`Handled`、`TargetKind`、`Target`、`TargetModel`、`Reason`。
- **关键限制**:`providersForExecution`(`sdk/api/handlers/handlers.go:1576`)在 `routeDecision.Provider != ""` 时把执行强制到该单一通道;`normalizeModelRouteResponse` 要求 provider 路由的 `Target` 非空。**不存在"保持原通道解析、仅替换模型"的路由响应形态** —— 因此配置必须显式给出目标通道。
- 插件协议:C ABI 动态库(`cliproxy_plugin_init` 导出),方法经 `model.route` 调用,envelope `{ok, result, error}`;`config_yaml` 在 register/reconfigure 时下发(插件自身配置子树 + 主机默认 `enabled`/`priority`)。参考实现:`examples/plugin/simple/go`。
- **config_yaml 下发形态(已从源码验证)**:主机经 `rpcLifecycleRequest{ConfigYAML []byte}`(JSON 中 `[]byte` 自动 base64 编码)下发,插件侧 `json.Unmarshal` 回 `[]byte` 字段即得 YAML 字节(`internal/pluginhost/rpc_schema.go`、`rpc_client.go`)。`enabled` 字段由主机**总是注入**(`normalizedConfigNode` 的 `ensureMappingScalar`):`plugins.enabled && plugins.configs.<id>.enabled` 都成立才为 true,否则 false(插件默认禁用,见 `internal/pluginhost/config.go`)。
- 插件文件发现规则(`internal/pluginhost/platform.go`):`plugins/<id>-v<version>.so`(版本号不能以 `v` 开头,如 `image-routing-v0.1.0.so`);`plugins/` 目录被 `.gitignore` 忽略。
- 图片内容结构(现状调研):各协议图片块形态不同 —— chat-completions `content[].type=="image_url"`;responses `input[].type=="input_image"`;Claude `content[].type=="image"`;Gemini `parts[].inlineData` / `parts[].fileData`。主线各转换器按字段判断,无集中检测函数可复用。
- **主机 SourceFormat 实际字符串(e2e 验证)**:`OpenAIAPIHandler.HandlerType()=="openai"`(/v1/chat/completions)、`OpenAIResponsesAPIHandler.HandlerType()=="openai-response"`(/v1/responses)、`ClaudeCodeAPIHandler.HandlerType()=="claude"`、`GeminiAPIHandler.HandlerType()=="gemini"`。插件 detectImage 的 switch 必须使用这些字符串。
- **openai-compat 通道的 provider key(e2e 验证)**:openai-compatibility 通道在 AvailableProviders 中以 `openai-compatible-<通道名>` 注册(`internal/util/provider.go` 的 `openAICompatibleProviderPrefix`);插件须按前缀容错匹配配置的 `fallback-provider`,且 Target 必须回传 AvailableProviders 中的实际 key,否则主机 `HasBuiltinProvider` 精确匹配失败。
- 用户自定义模型(deepseek-v4-flash、mimo-v2.5 等)在 registry 中无 `SupportedInputModalities` 能力元数据,自动能力探测不可行,必须用配置列表声明"不支持图片的模型"。

**配置草案(DP-0 已确认):**

```yaml
plugins:
  configs:
    image-routing:
      fallback: mimo-v2.5
      fallback-provider: opencode-go
      models: [deepseek-v4-flash]
```

## 目标与非目标

**目标:**
- 含图片的请求在请求模型被声明为不支持图片时,统一改路到配置的 fallback 模型,零主线代码改动。
- 插件行为可单测、可端到端验证;未命中时与未安装插件完全一致。

**非目标:**
- 自动能力探测(基于模型目录);fallback 二次回退链;响应模型名回写(force-mapping);/v1/images/* 端点;视频/音频检测。

## 决策

### 决策 1:以 ModelRouter 插件形式实现,而非核心内置

- **Choice**: 新建独立插件模块 `image-routing-plugin/`,实现 `model_router` 能力。
- **Rationale**: DP-0 明确约束"尽量不影响主线代码";调研确认主线 ModelRouter 管线完整可用(注册、调用、provider 路由、可用性校验均已实现),插件可拿到原始请求体与入口协议,方案零主线改动。
- **Alternatives**: 核心内置(在 `sdk/api/handlers` 增加检测+改路步骤,配置放顶层 `image-routing:`)。优点:配置形态与最初草图一致、所有构建可用;缺点:改动 `internal/config`、watcher diff、handlers 主线,违背用户约束;且 no-plugin 构建行为会与插件构建产生差异面。
- **Consequences**: 需要单独构建 .so(CGO);配置位于 `plugins.configs.image-routing`;无 CGO 的 no-plugin 构建不带此功能(用户部署需用默认 linux 构建)。

### 决策 2:插件源码位置为顶层 `image-routing-plugin/`

- **Choice**: 顶层独立 Go 模块目录,`go.mod` 用 `replace github.com/router-for-me/CLIProxyAPI/v7 => ..`(与 `examples/plugin/simple/go` 相同机制)。
- **Rationale**: 该插件是真实交付物而非示例;仓库已有顶层 addon 先例(`pi-cliproxy-provider/`);`plugins/` 目录被 gitignore(运行时加载目录),不能放源码。
- **Alternatives**: `examples/plugin/image-routing/`(定位是 demo,与交付物语义冲突);独立外部仓库(增加维护成本,且无法随主仓库 CI 测试)。
- **Consequences**: 仓库根目录新增一个模块;插件单测在模块内 `go test ./...` 独立跑。

### 决策 3:配置必须显式指定 `fallback-provider`(目标通道)

- **Choice**: `fallback-provider` 为必填字符串(允许为空字符串,为空时视为未启用该条件,路由返回 `Handled=false`)。
- **Rationale**: 调研确认执行路径强制单通道(`providersForExecution`);`ModelRouteResponse` 无"仅换模型、保持原通道解析"形态;插件无法得知请求模型会解析到哪个通道,也不能查询各通道模型列表。用户场景中 deepseek-v4-flash 与 mimo-v2.5 同在 `opencode-go` 通道,显式指定最明确。
- **Alternatives**: 为空时取 `AvailableProviders` 第一个(可能路由到不含 fallback 模型的通道,造成请求失败,语义脆弱,否决);插件经 host callback 查询模型归属(当前无该回调,需动主线,违背约束,否决)。
- **Consequences**: 配置比最初草图多一个字段;文档中明确说明。

### 决策 4:检测范围覆盖四种入口协议

- **Choice**: chat-completions(`image_url`)、responses(`input_image`)、claude(`image` 块)、gemini(`inlineData`/`fileData`)四类都实现。
- **Rationale**: "统一改路"语义要求无论客户端走哪个入口协议都生效;每类都是数行 gjson 字段判断,成本低;主场景(用户 opencode)是 chat-completions,作为核心测试路径。
- **Alternatives**: 仅 chat-completions(实现最简,但 gemini/claude 入口的请求不覆盖,"统一"名不副实,否决)。
- **Consequences**: 插件代码多一个 switch 分支与对应测试;检测函数签名统一为 `detectImage(body []byte, sourceFormat string) bool`。

### 决策 5:改路后响应模型名保持 fallback 模型名,不做回写

- **Choice**: 不重写响应中的模型字段;客户端看到的响应模型名为 fallback 模型。
- **Rationale**: force-mapping 回写依赖主线别名机制的 `ForceMapping` 标记,插件侧无此能力;opencode 等客户端不依赖响应模型名执行逻辑。
- **Alternatives**: 主线加"路由决策响应回写"钩子(违背零改动约束,否决)。
- **Consequences**: 使用方在日志/用量统计中看到的是 fallback 模型名,属预期行为,文档说明。

### 决策 6:fallback-provider 不可用时的失败语义

- **Choice**: `fallback-provider` 不在 `AvailableProviders` 时返回 `Handled=false`,请求走原路径(原模型)。
- **Rationale**: 不放大故障:改路目标暂时不可用不应让请求直接失败,退回原行为最安全;插件日志记录跳过的原因。
- **Alternatives**: 返回错误(请求失败,用户困惑,否决);换其它可用通道(无模型归属信息,不可靠,否决)。
- **Consequences**: 场景表现为"改路静默失效",文档注明并通过插件日志排查。

## 风险与验证

| 风险 | 影响 | 缓解/验证 |
|---|---|---|
| 误检/漏检图片块 | 改路错乱 | 检测基于结构化字段(`type`/`inlineData`/`fileData`),非字符串搜索;单测覆盖 4 协议正反例 |
| 插件构建环境无 CGO | 无法构建 .so | 文档写明要求;主仓库 linux 默认构建本身支持 CGO |
| 配置热加载行为 | 配置变更后决策漂移 | 主机 reconfigure 机制天然支持(config_yaml 重新下发),插件每次 `model.route` 使用最新解析结果;单测覆盖非法 YAML 保持旧配置 |
| 改路后模型不可用(配额等) | 请求失败 | 属于 fallback 模型的正常失败路径,主机已有重试/冷却机制;`Handled=false` 语义保证不引入新失败面 |
| 与其它 ModelRouter 插件共存 | 决策顺序 | 主机按插件优先级依次调用;本插件仅处理命中条件,未命中返回 `Handled=false` 让位后续插件,无需额外协调 |

## 关键假设与失效条件

以下假设均已在调研阶段从源码验证;若在实现/执行中发现某一项为假,按对应路径调整方案:

| # | 假设(已验证) | 失效条件 | 失效时的调整路径 |
|---|---|---|---|
| A1 | 主线在模型→provider 解析前调用 `model.route`(`applyModelRouter`,见 `sdk/api/handlers/handlers.go`) | 调用点不在该阶段 | 改路会发生在错误阶段 → 重新评估核心内置方案(方案 B) |
| A2 | `ModelRouteRequest.Body` 是原始客户端请求体(未翻译) | Body 为翻译后载荷 | 检测逻辑需按翻译后结构调整,或改路由到核心方案 |
| A3 | `routeDecision.Provider` 非空时执行强制单通道(`providersForExecution`) | 存在"仅换模型"路径 | 配置可简化去掉 `fallback-provider`,按新路径实现 |
| A4 | 插件 C ABI 形状与导出符号名与主机一致(参照 `examples/plugin/simple/go` + `pluginabi.ABIVersion`) | 符号/结构不匹配 | 插件无法加载或崩溃 → 按主机当前 `internal/pluginhost/loader_unix.go` 的 C 头对齐 |
| A5 | `thinking.ParseSuffix` 可从插件模块导入(模块路径前缀允许 internal 导入) | 编译失败 | 插件内实现本地后缀剥离函数 |
| A6 | 主机以 JSON 内嵌 base64 YAML 下发 `config_yaml`(`rpcLifecycleRequest`) | 载荷形态不同 | 按实际形态修正 `applyConfig` 解析(需同时更新 T1 测试) |
| A7 | `pluginabi.MethodPluginRegister/Reconfigure/ModelRoute` 等常量存在 | 编译失败 | 按 `sdk/pluginabi/types.go` 实际常量名修正 |
| A8 | 主机 SourceFormat 字符串为 `openai`/`openai-response`/`claude`/`gemini`;openai-compat 通道在 AvailableProviders 中带 `openai-compatible-` 前缀(e2e 已验证) | 另有其它入口字符串或前缀规则 | 按 e2e 观测的实际值修正 detectImage 的 switch 与 matchAvailableProvider 的匹配规则,并更新对应测试 |

## 验证方式

- 单元测试:插件模块内 `go test ./...`(配置解析、四协议检测、决策矩阵、方法 envelope)。
- 端到端:`go build -buildmode=c-shared -o plugins/image-routing-v0.1.0.so .` → 配置启用 → 起服务 → curl 构造含/不含 image_url 请求,对比请求日志中的实际模型与通道;禁用插件复测确认行为不变。
