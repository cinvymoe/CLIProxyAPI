# image-routing 插件

将含图片内容的请求从「不支持图片的模型」改路到指定通道的 fallback 模型。请求模型在 `models` 列表中且请求体包含图片内容时,改路到 `fallback-provider` 通道的 `fallback` 模型;未命中(模型不在列表、请求无图片、fallback 通道不可用)时行为与未安装插件完全一致。

## 支持入口协议

| 入口 | 图片字段 |
| --- | --- |
| chat-completions | `image_url`(content 数组元素) |
| responses | `input_image` |
| Claude | `image` |
| Gemini | `inlineData` / `fileData` |

## 构建

需要 CGO 与 Go 1.26+:

```bash
cd image-routing-plugin && go mod tidy && CGO_ENABLED=1 go build -buildmode=c-shared -o ../plugins/image-routing-v0.1.0.so .
```

## 安装

将 `plugins/image-routing-v0.1.0.so` 放到服务运行目录的 `plugins/` 下即可。文件名即插件 ID 与版本(`<id>-v<version>.so`,主机按此发现插件);`plugins/` 默认被 gitignore,不会进入仓库。

## 配置

在 `config.yaml` 顶层增加(注意缩进,与 `api-keys`、`port` 同级):

```yaml
plugins:
  enabled: true
  configs:
    image-routing:
      enabled: true
      fallback: mimo-v2.5
      fallback-provider: opencode-go
      models: [deepseek-v4-flash]
```

字段说明:

- `plugins.enabled`:主机级插件开关,必须为 `true` 才会加载插件(默认禁用)。
- `configs.image-routing.enabled`:本插件开关,必填为 `true` 才生效(插件默认禁用)。
- `fallback`:改路目标模型。
- `fallback-provider`:改路目标通道(必填)。服务需已配置该通道且凭证可用;若该通道不在当前可用通道中,改路静默跳过(请求按未命中处理)。
- `models`:视为不支持图片的模型列表;匹配时去除思考后缀(`/think` 等)、大小写不敏感。

## 说明

- 改路后,响应与用量统计中的模型名为 fallback 模型。
- 主程序需要以 CGO 构建(插件为 c-shared 加载);no-plugin 构建不加载插件。
- 重启服务后确认日志出现插件加载与配置应用记录:`pluginhost: plugin loaded` 与 `image-routing: config applied ...`。
