# 能力规格:image-routing

## Purpose

定义 image-routing 插件的可测试行为:图片请求改路、检测范围、决策条件、不处理路径与插件协议合规。该插件不修改任何主线代码,所有行为都通过官方 ModelRouter 插件能力实现。

## ADDED Requirements

### Requirement: 插件配置解析

image-routing 插件 SHALL 在注册(register)与重配置(reconfigure)时,从主机下发的 `config_yaml`(YAML)中解析配置:字符串 `fallback`、字符串 `fallback-provider`、字符串数组 `models`、布尔 `enabled`。任何字段缺失时按零值处理(`models` 缺失视为空列表,`fallback`/`fallback-provider` 缺失视为空字符串,`enabled` 缺失视为 false —— 与主机"插件默认禁用"的语义一致;主机总是会在下发的 config_yaml 中注入显式 `enabled` 字段,见 design.md)。解析失败不得导致插件崩溃。

#### Scenario: 完整配置

- **WHEN** config_yaml 为 `enabled: true\nfallback: mimo-v2.5\nfallback-provider: opencode-go\nmodels:\n  - deepseek-v4-flash\n`
- **THEN** 解析结果为 fallback="mimo-v2.5"、fallback-provider="opencode-go"、models=["deepseek-v4-flash"]、enabled=true

#### Scenario: 缺失字段

- **WHEN** config_yaml 仅为 `enabled: true`
- **THEN** 解析结果为 fallback=""、fallback-provider=""、models 为空列表、enabled=true,且不报错

#### Scenario: enabled 字段缺失

- **WHEN** config_yaml 仅为 `fallback: mimo-v2.5`
- **THEN** 解析结果为 enabled=false(改路不生效),其余字段按值解析,不报错

#### Scenario: 非法 YAML

- **WHEN** config_yaml 为非法 YAML 字节
- **THEN** 插件保持上一次成功解析的配置(首次加载即失败时保持默认配置,即全零值),且后续 `model.route` 调用仍返回正常 envelope(Handled=false),不崩溃

### Requirement: chat-completions 图片内容检测

image-routing 插件 SHALL 对 `SourceFormat` 为 `openai`(主机对 /v1/chat/completions 入口传入的实际字符串,即 `OpenAIAPIHandler.HandlerType()`)的请求体,检测任意 `messages[].content[]` 数组元素中是否存在 `type=="image_url"` 的块。命中即视为"请求含图片"。`content` 为纯字符串(无数组)时视为不含图片。

#### Scenario: 消息含 image_url 块

- **WHEN** SourceFormat 为 `openai`,请求体为 `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]}`
- **THEN** 检测结果为"含图片"

#### Scenario: 纯文本消息

- **WHEN** 请求体 `content` 为字符串或只有 `type=="text"` 块
- **THEN** 检测结果为"不含图片"

#### Scenario: 图片块位于工具结果消息中

- **WHEN** 请求体某条 `role=="tool"` 消息的 `content[]` 中存在 `type=="image_url"` 块
- **THEN** 检测结果为"含图片"

### Requirement: responses 格式图片内容检测

image-routing 插件 SHALL 对 `SourceFormat` 为 `openai-response`(主机对 /v1/responses 入口传入的实际字符串,即 `OpenAIResponsesAPIHandler.HandlerType()`)的请求体,检测 `input[]` 数组中是否存在 `type=="input_image"` 的元素。命中即视为"请求含图片"。

#### Scenario: input 含 input_image

- **WHEN** SourceFormat 为 `openai-response`,请求体为 `{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`
- **THEN** 检测结果为"含图片"

#### Scenario: input 仅含文本

- **WHEN** 请求体 `input` 中只有 `input_text`/`message` 元素
- **THEN** 检测结果为"不含图片"

### Requirement: Claude 格式图片内容检测

image-routing 插件 SHALL 对 `SourceFormat=claude` 的请求体,检测任意 `messages[].content[]` 数组中是否存在 `type=="image"` 的块。命中即视为"请求含图片"。

#### Scenario: content 含 image 块

- **WHEN** 请求体为 `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`
- **THEN** 检测结果为"含图片"

#### Scenario: content 仅含文本块

- **WHEN** 请求体 `content[]` 中只有 `type=="text"` 块
- **THEN** 检测结果为"不含图片"

### Requirement: Gemini 格式图片内容检测

image-routing 插件 SHALL 对 `SourceFormat=gemini` 的请求体,检测任意 `contents[].parts[]` 数组中是否存在带 `inlineData` 或 `fileData` 键的 part。命中即视为"请求含图片"。

#### Scenario: part 含 inlineData

- **WHEN** 请求体为 `{"model":"deepseek-v4-flash","contents":[{"role":"user","parts":[{"text":"hi"},{"inlineData":{"mimeType":"image/png","data":"AA=="}}]}]}`
- **THEN** 检测结果为"含图片"

#### Scenario: part 仅含文本

- **WHEN** 请求体 `parts[]` 中只有 `text` 键
- **THEN** 检测结果为"不含图片"

### Requirement: 改路决策

image-routing 插件 SHALL 对 `model.route` 请求做如下决策:当且仅当 (a) 配置 `enabled=true` 且 `fallback`、`fallback-provider` 均非空且 `models` 非空;(b) 请求模型去掉思考后缀后(大小写不敏感)∈ `models`;(c) 按 SourceFormat 检测请求体含图片;(d) `fallback-provider` 在 `AvailableProviders` 中可用(openai-compat 通道在 AvailableProviders 中以 `openai-compatible-<通道名>` 形式出现,插件 SHALL 对配置值做前缀容错匹配,即配置 `opencode-go` 可匹配 `openai-compatible-opencode-go`)—— 返回 `Handled=true, TargetKind=provider, Target=<AvailableProviders 中的实际 key>, TargetModel=fallback`。任一条件不满足,SHALL 返回 `Handled=false`。

#### Scenario: 命中改路

- **WHEN** 配置为 fallback="mimo-v2.5"、fallback-provider="opencode-go"、models=["deepseek-v4-flash"],请求模型为 `deepseek-v4-flash`,请求体含 image_url,AvailableProviders 含 `openai-compatible-opencode-go`
- **THEN** 返回 `{Handled:true, TargetKind:"provider", Target:"openai-compatible-opencode-go", TargetModel:"mimo-v2.5"}`

#### Scenario: 思考后缀模型命中

- **WHEN** 请求模型为 `deepseek-v4-flash(high)`(思考后缀),其余同上
- **THEN** 返回 `{Handled:true, TargetKind:"provider", Target:"openai-compatible-opencode-go", TargetModel:"mimo-v2.5"}`

#### Scenario: 列表外模型含图不处理

- **WHEN** 请求模型为 `glm-5.1`(不在 models 列表),请求体含 image_url
- **THEN** 返回 `Handled=false`

#### Scenario: 列表内模型不含图不处理

- **WHEN** 请求模型为 `deepseek-v4-flash`,请求体为纯文本
- **THEN** 返回 `Handled=false`

#### Scenario: fallback-provider 不可用不处理

- **WHEN** 请求模型在列表内且含图,但 `fallback-provider`(含前缀容错匹配后)不在 AvailableProviders 中
- **THEN** 返回 `Handled=false`

#### Scenario: 大小写不敏感匹配

- **WHEN** 请求模型为 `DeepSeek-V4-Flash`,models=["deepseek-v4-flash"],其余命中条件满足
- **THEN** 返回 `{Handled:true, TargetKind:"provider", Target:"openai-compatible-opencode-go", TargetModel:"mimo-v2.5"}`

#### Scenario: 插件未启用

- **WHEN** 配置 `enabled=false`
- **THEN** 任何请求都返回 `Handled=false`

### Requirement: 流式与非流式一致性

image-routing 插件 SHALL 对流式与非流式请求(请求 `Stream` 字段不同)应用完全相同的检测与决策逻辑;`Stream` 字段不参与判断。

#### Scenario: 流式请求命中

- **WHEN** 请求 `Stream=true` 且命中改路条件
- **THEN** 返回 `{Handled:true, TargetKind:"provider", Target:"openai-compatible-opencode-go", TargetModel:"mimo-v2.5"}`

#### Scenario: 非流式请求未命中

- **WHEN** 请求 `Stream=false` 且请求体为纯文本
- **THEN** 返回 `Handled=false`

### Requirement: 插件能力注册与协议合规

image-routing 插件 SHALL 在注册响应中声明 `model_router` 能力,SHALL 处理 `model.route` 方法,响应使用标准 envelope(`{ok, result, error}`),`result` 为 `ModelRouteResponse` 的 JSON(`Handled`、`TargetKind`、`Target`、`TargetModel`、`Reason`)。对 `plugin.register` 与 `plugin.reconfigure` 两个生命周期方法,SHALL 返回相同结构的注册 envelope(能力声明一致),其中 `reconfigure` 同时应用新下发的配置。对未实现的方法 SHALL 返回 error envelope,不崩溃。

#### Scenario: 注册声明能力

- **WHEN** 主机以 `plugin.register` 调用插件
- **THEN** 响应 envelope 的 `result.capabilities.model_router == true`

#### Scenario: 正常路由响应

- **WHEN** 主机以 `model.route` 调用并传入标准 `ModelRouteRequest`(含 body、source_format、requested_model、available_providers)
- **THEN** 响应 envelope 的 `ok == true` 且 `result` 为合法 `ModelRouteResponse` JSON

#### Scenario: 未知方法

- **WHEN** 主机以未知方法名调用插件
- **THEN** 响应 envelope 的 `ok == false` 且带 `error.code`/`error.message`,进程不退出

### Requirement: 主机集成(零主线改动验证)

当插件被加载且启用时,主机 SHALL 在模型→provider 解析前调用该插件的 `model.route`;当插件未加载、未启用或返回 `Handled=false` 时,主机对请求的处理 SHALL 与未安装插件时完全一致。

#### Scenario: 插件启用时改路生效

- **WHEN** 插件 .so 位于 `plugins/` 且配置启用,客户端向 `deepseek-v4-flash` 发送含 image_url 的 chat 请求
- **THEN** 该请求实际由 `fallback-provider` 通道的 `fallback` 模型执行(可经请求日志/上游观测验证)

#### Scenario: 插件禁用时行为不变

- **WHEN** 插件配置 `enabled=false` 或 .so 不存在
- **THEN** 含 image_url 的 chat 请求行为与未安装插件完全一致(模型不变,不报改路相关错误)
