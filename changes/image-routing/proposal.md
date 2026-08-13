# 变更提案:image-routing 图片请求改路插件

## 为什么 (Why)

当前 CLIProxyAPI 没有任何"按请求内容改路"的能力:模型选择只依赖模型名(静态别名映射、失败重试池、轮询)。当客户端(如 opencode)在对话中上传图片时,图片以 `image_url` 数据块(base64 data URL)的形式出现在 `/v1/chat/completions` 请求的 `messages[].content[]` 中——请求仍会发给请求的模型(如 deepseek-v4-flash)。deepseek-v4-flash 这类不支持图片输入的模型会直接报错,用户只能手动切换模型重发。

用户需要:当请求包含图片内容且请求模型被配置为"不支持图片"时,**统一**把该请求改路到配置的 fallback 模型(如 mimo-v2.5),全程无感;并且**不改动主线代码**(插件形式)。

## 变化 (What Changes)

新增一个独立的 ModelRouter 插件(`image-routing-plugin/`,Go + C ABI 动态库):

1. 通过官方 `model_router` 插件能力注册路由;主线在模型→provider 解析和凭证选择**之前**自动调用 `model.route`(现有管线,零主线改动)。
2. 插件拿到原始请求体(`Body`)、入口协议(`SourceFormat`)、请求模型(`RequestedModel`)、可用通道列表(`AvailableProviders`)。
3. 检测逻辑:按入口协议识别图片内容块(chat-completions `image_url`、responses `input_image`、Claude `image`、Gemini `inlineData`/`fileData`)。
4. 决策:请求模型(去思考后缀、大小写不敏感)∈ `models` 列表 且 含图片 且 `fallback-provider` 可用 → 返回 `TargetKind=provider`、`Target=fallback-provider`、`TargetModel=fallback`;否则 `Handled=false` 完全走原路径。

配置(位于 `plugins.configs.image-routing`;注意主机默认禁用插件,`enabled: true` 必须显式配置):

```yaml
plugins:
  configs:
    image-routing:
      enabled: true
      fallback: mimo-v2.5          # 图片请求改路目标模型
      fallback-provider: opencode-go  # 目标通道(必填,见 design.md 决策 3)
      models: [deepseek-v4-flash]  # 视为不支持图片的模型;不在此列表则不动
```

## 范围 (Scope)

### In
- `image-routing-plugin/` 插件源码(独立 Go 模块):cgo ABI 框架、配置解析、图片内容检测、路由决策、`model.route` 方法处理。
- 插件单元测试(检测器、决策矩阵、配置解析、方法 envelope)。
- 插件构建说明(README)与配置示例片段。
- 端到端验证(构建 .so → 加载 → 请求改路)。

### Out
- **任何主线代码改动**(`internal/`、`sdk/`、`cmd/` 零修改)。
- 基于 `SupportedInputModalities` 的自动能力探测(用户自定义模型无能力元数据,依赖配置列表)。
- `/v1/images/*` 端点路由(那是图片生成,与本需求无关)。
- 视频/音频内容检测。
- fallback 模型不可用时的二次回退链(失败走原路径即可)。
- 响应模型名 force-mapping 回写(响应中模型名即 fallback 模型名,客户端不依赖)。

## 影响 (Impact)

- 新增目录 `image-routing-plugin/`(git 跟踪),参考 `examples/plugin/simple/go` 的构建机制(`replace` 指向仓库根)。
- `plugins/` 目录是运行时加载目录且被 gitignore,构建产物放那里不入库。
- 主线行为:未安装/未启用插件时,与现在**完全一致**;插件 `Handled=false` 时也完全一致。
- no-plugin(无 CGO)构建不受影响(只是不会加载任何插件)。

## 完成证明 (Proof)

1. `cd image-routing-plugin && go test ./...` 全部通过(检测器、决策矩阵、配置解析)。
2. 构建出 `image-routing-v0.1.0.so` 放入 `plugins/`,配置 `fallback: mimo-v2.5`、`fallback-provider: opencode-go`、`models: [deepseek-v4-flash]`。
3. 端到端:向 `deepseek-v4-flash` 发送**含** `image_url` 块的 chat 请求 → 请求日志/上游显示实际执行模型为 `mimo-v2.5`;同一模型**不含**图请求 → 仍走 `deepseek-v4-flash`。
4. 模型列表外的模型(`glm-5.1`)发含图请求 → 行为不变。
5. 插件 `enabled: false` 时含图请求行为与未安装插件一致(回归证明零主线影响)。
