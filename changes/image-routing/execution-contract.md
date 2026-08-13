# 执行合同:image-routing 图片请求改路插件

## Intent Lock

- **变更名称**:image-routing
- **要解决的问题**:客户端(如 opencode)在对话中上传图片时,图片以 `image_url` 等数据块出现在请求体中,请求仍发给不支持图片输入的模型(如 deepseek-v4-flash),导致失败,用户只能手动切换模型。需要按请求内容把含图请求**统一**改路到配置的 fallback 模型(如 mimo-v2.5),且**零主线代码改动**。
- **范围内**:`image-routing-plugin/` 独立 Go 模块(ModelRouter 插件):cgo ABI 框架、配置解析、四协议图片检测(chat-completions/responses/Claude/Gemini)、改路决策、`model.route` 方法处理、插件单测、README 与构建说明、端到端验证。
- **范围外**:任何主线代码改动(`internal/`、`sdk/`、`cmd/` 零修改);基于 `SupportedInputModalities` 的自动能力探测;fallback 二次回退链;响应模型名 force-mapping 回写;`/v1/images/*` 端点;视频/音频内容检测。

## Approved Behavior

- **已批准需求摘要**(specs/image-routing/spec.md 共 8 项 Requirement):
  1. 配置解析(`fallback`/`fallback-provider`/`models`/`enabled`,缺失按零值,enabled 缺失=false,非法 YAML 保持旧配置/默认配置)
  2. chat-completions 检测(`content[]` 中 `type=="image_url"`,含 tool 消息)
  3. responses 检测(`input[]`/`content[]` 中 `type=="input_image"`)
  4. Claude 检测(`content[]` 中 `type=="image"`)
  5. Gemini 检测(`parts[]` 含 `inlineData`/`fileData`)
  6. 改路决策(在列表+含图+provider 可用 → `Handled=true, TargetKind=provider, Target=fallback-provider, TargetModel=fallback`;任一条件不满足 → `Handled=false`;去思考后缀、大小写不敏感)
  7. 流式/非流式一致性(`Stream` 不参与判断)
  8. 能力注册与协议合规(`model_router` 能力;`model.route` 标准 envelope;register/reconfigure 相同结构响应;未知方法 error envelope 不崩溃)
- **关键场景**:命中改路(deepseek-v4-flash + image_url → opencode-go / mimo-v2.5);思考后缀模型;大小写不敏感;列表外模型含图不处理;列表内模型纯文本不处理;fallback-provider 不可用不处理;插件未启用不处理;禁用后行为与未安装一致。
- **验收检查**:
  1. `cd image-routing-plugin && go test ./...` 全绿(gofmt -l 无输出);
  2. `.so` 构建成功并加载,日志出现 `image-routing: config applied`;
  3. e2e 四场景(命中/不命中/列表外/禁用)行为符合预期;
  4. `git status --porcelain -- internal/ sdk/ cmd/` 与 `git diff --stat -- internal/ sdk/ cmd/` 均无输出(零主线改动)。

## Design Constraints

- **架构约束**:只通过官方 ModelRouter 插件能力实现;插件模块 `image-routing-plugin/` 独立 `go.mod`(`replace` 指向仓库根);不注册任何其它能力(仅 `model_router`);未命中时 `Handled=false` 让位后续插件。
- **接口约束**:C ABI 导出(`cliproxy_plugin_init`/`cliproxyPluginCall`/`cliproxyPluginFree`/`cliproxyPluginShutdown`),ABI/Schema 版本取 `pluginabi.ABIVersion`/`pluginabi.SchemaVersion`;`model.route` 请求为 `ModelRouteRequest`(Body 经 JSON base64)、响应为 `ModelRouteResponse`,均包标准 envelope;`config_yaml` 为 JSON 内嵌 base64 YAML,`enabled` 由主机总是注入(遵循主机值,缺失按 false)。
- **依赖约束**:仅新增插件模块依赖(gjson、logrus、yaml.v3、主模块 replace);可导入 `internal/thinking` 的 `ParseSuffix`(模块路径前缀允许);不得因此改动主 `go.mod`/`go.sum`。
- **数据约束**:配置字段 `fallback`(string)、`fallback-provider`(string)、`models`(YAML 数组)、`enabled`(bool);模型匹配去思考后缀、大小写不敏感;`fallback-provider` 必须出现在 `AvailableProviders` 才改路。

## Execution Plan

full 流程:先运行 `ssf execution recommend <change-dir>`(已执行,见下),按任务量与 wave 策略列出可用方式并推荐;用户通过 `--confirm` 明确确认模式后,`ssf execution plan <change-dir> --mode <mode> --confirm --reason <text> --wave <id>:<parallel|serial>:<task,...>[:<depends-on,...>]` 持久化执行计划到 `<change>/.superpowers/sdd/execution-plan.json`(该 JSON 是计划控制面,不属于本合同)。Batch Inline 为串行模式。

## Execution Waves

每个 wave 必须有唯一 ID;后续 wave 仅当依赖 wave 的 review receipt 为 `pass` 后才可开始。

### Wave 1

- **Wave ID**:w1-skeleton-detect
- **任务**:T1(插件骨架与配置解析)、T2(图片内容检测器)
- **依赖 wave**:无
- **策略**:`parallel`(T1 的 main.go/config.go 与 T2 的 detect.go 为不同新文件,无写冲突;不支持并发派发时须报告并按 serial 执行)
- **目标**:插件模块可测试的骨架 + 四协议检测器。
- **输入**:tasks.md T1/T2 全部代码与测试。
- **输出**:`image-routing-plugin/go.mod`、`main.go`、`config.go`、`detect.go` 及对应测试;`go test ./...` 通过。
- **完成标准**:`cd image-routing-plugin && go mod tidy && gofmt -l . && go test ./...` 全绿;T1 的 `model.route` 占位返回 `Handled=false`。
- **Review gate**:`ssf execution review --wave w1-skeleton-detect --base <sha> --head <sha> --report <path> --verdict pass|fail`

### Wave 2

- **Wave ID**:w2-decision
- **任务**:T3(路由决策与 model.route 接线)
- **依赖 wave**:w1-skeleton-detect
- **策略**:`serial`
- **目标**:决策函数与 `model.route` 完整接线,替换 T1 占位。
- **输入**:tasks.md T3 的 `route.go`、`handleModelRoute`、`route_test.go`。
- **输出**:`route.go` + `route_test.go`;决策矩阵(含后缀/大小写/不可用/禁用/流式)全绿。
- **完成标准**:`cd image-routing-plugin && go test ./...` 全绿;`handleMethod` 的 `model.route` 返回合法 envelope。
- **Review gate**:`ssf execution review --wave w2-decision --base <sha> --head <sha> --report <path> --verdict pass|fail`

### Wave 3

- **Wave ID**:w3-build-e2e
- **任务**:T4(构建、文档与端到端验证)
- **依赖 wave**:w2-decision
- **策略**:`serial`
- **目标**:.so 构建、README、e2e 四场景验证、零主线改动证明。
- **输入**:tasks.md T4 全部步骤。
- **输出**:`image-routing-plugin/README.md`;构建产物;e2e 实测输出(响应 model 字段/日志);git 主线零改动断言结果。
- **完成标准**:T4 步骤 2-5 全部通过(构建、加载、四场景、git 检查)。
- **Review gate**:`ssf execution review --wave w3-build-e2e --base <sha> --head <sha> --report <path> --verdict pass|fail`

## Test Obligations

- **必须先从失败测试开始的行为**:T1 配置解析与方法 envelope 测试、T2 检测器表驱动测试、T3 决策矩阵测试(先写测试,再实现)。
- **必需的边界情况**:字符串 content(非数组)、tool 消息内 `image_url`、`input_image` 位于 message content 内、Gemini `fileData`、未知 SourceFormat、非法 YAML 保持旧配置/首次加载默认配置、enabled 缺失、fallback-provider 不在 AvailableProviders、思考后缀、大小写不敏感、流式请求。
- **回归敏感区域**:主线行为零改动(插件未启用/未命中时与未安装完全一致)——e2e 禁用场景 + git 主线目录检查双重验证;插件共存(未命中 `Handled=false` 让位)。

## Execution Mode

- **可用方式与推荐**:`ssf execution recommend changes/image-routing` 已执行 → 推荐 **sdd**(任务数 4 超过 inline 阈值 3;wave 间有依赖需要 gate)。可用:inline、batch-inline、sdd。
- **用户确认的模式**:待 DP-3 确认(sdd 为推荐;选择非推荐模式须 `--acknowledge-recommendation`)。
- **推荐理由 / 项目事实**:4 个任务、3 个 wave、wave 间依赖(检测→决策→e2e),需要逐 wave review gate;sdd 提供每 wave 独立评审收口。
- **非推荐选择的风险确认**:`--acknowledge-recommendation`(若适用)。
- **执行计划命令**:`ssf execution plan changes/image-routing --mode <mode> --confirm --reason <text> --wave w1-skeleton-detect:parallel:T1,T2 --wave w2-decision:serial:T3:w1-skeleton-detect --wave w3-build-e2e:serial:T4:w2-decision [--acknowledge-recommendation]`
- **允许的修订**:`ssf execution revise <change-dir> --mode sdd --confirm ...` 保留/升级为 sdd;不允许降级。
- **计划 revision / artifact hash**:由 `ssf execution plan` 记录。

## Verification Dimensions

| 维度 | 状态 | 发现 |
|------|------|------|
| Completeness | Pending | 8 项 Requirement 全覆盖(T1-T4 映射见 tasks.md 交付映射表) |
| Correctness | Pending | 待 wave review + e2e |
| Coherence | Pending | 待执行验证 |

**总体结论**:Pending

## Review Gates

- **强制审查点**:每个 Execution Wave 完成后记录 `ssf execution review` 的 review receipt(`pass` 后方可进入依赖 wave 或收口)。
- **阻塞类别**:依赖 wave 未通过、review receipt 为 `fail`、缺失或过期;设计假设 A1-A7(design.md)被证伪且无替代路径。
- **收口条件**:所有当前 wave 都有 `pass` review receipt;e2e 四场景验证完成;主线目录零改动。

## Escalation Rules

- **何时回退到 `specifying`**:需求/场景变化(如新增入口协议、改路语义变化);检测格式假设(A2/A6)在实现中被证伪且无法在插件内调整。
- **何时回退到 `bridging`**:契约与 artifacts 失配(范围、约束、wave 划分变化);配置形态被主机行为证伪(如 `config_yaml` 载荷形态与 A6 不符)。
- **何时不得继续实现**:无 DP-3 记录;关键假设(A1-A7)任一被证伪且无替代路径;e2e 验证失败且无法在 wave 内修复;review receipt 为 `fail`。
