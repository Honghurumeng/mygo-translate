# MyGo 翻译

把 [Codex Switch](../codex-switch) 里那个翻译弹窗单独拿出来，用 [MyGo](https://github.com/egoist/mygo) 的原生 UI 重写的桌面应用：Go 画界面，没有 WebView，没有 HTML / JavaScript，也没有前端构建。

![翻译完成](docs/translated.png)

翻译中是另一副样子：进度条、`x/y 段`、取消按钮，`docs/translating.png` 是那个状态。

## 功能

- 原文 / 译文双栏，原文框里按 `⌘V` 直接粘贴截图即可识别文字（macOS Vision）
- 33 种目标语言，原始语言可选「自动检测」，检测结果会标在原文标题旁
- 一键反转语言和文本
- 长文本按句子切成不超过 4500 字符的段，2 段并发翻译，按原顺序拼回
- Google 翻译为主，失败时自动改用 MyMemory；可重试的错误最多重试 3 次（1s / 2s / 4s 退避）
- 底部实时显示「正在翻译 x/y 段」、重试提示与分段预估，翻译中可取消
- 一键复制译文，`⌘↩` 翻译，`Esc` 取消

## 构建与运行

需要 Go 1.27+。

```bash
go tool mygo dev     # 开发模式运行
go tool mygo build   # 打包到 build/（.app 与 .dmg）
go test ./...        # 跑测试
```

## 图标

`resources/icon.png` 是 1024×1024 的图：一块蓝色圆角方，中间一个白色的宋体「译」。方块按 macOS 的图标网格画（824 居中、圆角 185），`mygo build` 会把它编成 `.icns` 放进 `.app`，也是 dmg 的卷图标。

图是代码画出来的，不是手工导出的，改了要重跑：

```bash
go run ./tools/icongen                        # 写 resources/icon.png
go run ./tools/icongen -ink 0.40 -top '#3f83ff' -bottom '#0a2f9e'
```

字形取自系统的 Songti SC（`/System/Library/Fonts/Supplemental/Songti.ttc`），`-face` 换字重、`-char` 换字。为了小尺寸下笔画不糊，字重用的是 Bold，字按墨迹外接框居中而不是按字身框。

## 代码结构

```
main.go               入口；这里有一条 //go:debug 指令，见下文
internal/app          状态与界面：窗口、语言选择、双栏编辑区、状态栏
  app.go              状态、翻译任务、取消、图片识别、复制
  view.go             整页布局，全部由 ui 包绘制
  lang.go             语言表与数字格式化
internal/translate    翻译内核：分段、Google / MyMemory、重试、并发
internal/ocr          macOS Vision 图片识字（经 osascript 的 JXA 桥，无 cgo）
tools/icongen         画应用图标：超采样 + Songti 字形，纯 Go
```

## 从 Codex Switch 搬过来的东西

| 原实现（src/App.tsx） | 这里 |
| --- | --- |
| `splitTranslationText` / `nextTranslationChunkLength` | `translate.Split`，按 rune 计数，切点顺序：空行 → 句末标点 → 换行 → 空格 → 硬切 |
| `requestGoogle`（两个端点依次尝试） | `Client.requestGoogle` |
| `requestMyMemory`（450 字符一段、`auto` 按文字猜源语言） | `Client.requestMyMemory`、`guessMyMemorySource` |
| `translateChunkWithRetry`（3 次、指数退避、保留首尾空白） | `Client.translateChunk` |
| `translateChunks`（并发 2、按段号回填、语言按长度投票） | `Client.Translate` |
| Tauri 命令 `ocr_image`（macOS Vision） | `internal/ocr`，同一套语言列表 |
| 弹窗里的状态文案与按钮 | 底部状态行：进度、重试、字符数与段数 |

## 两个踩过的坑

**HTTP/2 会被 Google 当成机器人。** 同一份请求，用 Go 1.27 默认的 HTTP/2 客户端发出去，`translate.googleapis.com` 一律回 429 的 "Sorry..." 页面（TLS 指纹问题）；走 HTTP/1.1 就正常。整个应用只跟翻译接口说话，所以 `main.go` 里用 `//go:debug http2client=0` 一次性关掉 HTTP/2 客户端。这是编译期指令，删掉它翻译会退化成只用 MyMemory。

**图片识别不引入 cgo。** MyGo 的应用要求 `CGO_ENABLED=0`，所以没有链接 Vision.framework，而是把 PNG 写到临时文件，用 `osascript -l JavaScript` 通过 ObjC 桥跑一次 `VNRecognizeTextRequest`，再把 JSON 读回来。一次约 0.6 秒，不需要用户安装任何东西。非 macOS 平台按钮不可用。

## 已知取舍

- 只做了翻译这一件事，没有保留配置切换、托盘、多窗口
- 译文框只读；编辑原文或切换语言会清掉上一次的译文（与原实现一致）
- 图片识别只认 macOS；Windows / Linux 上按钮会灰掉
- Google / MyMemory 都是免费接口，量大时会被限流，应用会退避重试并提示
