// MyGo 翻译：把 Codex Switch 里的翻译弹窗做成原生的桌面应用。

// Google 的免费翻译接口会把 Go 1.27 的 HTTP/2 客户端当成机器人，直接回
// 429（"Sorry..." 那一页）；同一份请求走 HTTP/1.1 就正常。整个应用只跟
// 翻译接口说话，所以在这里一次性关掉 HTTP/2 客户端。
//go:debug http2client=0

package main

import (
	"log"

	"mygo-translate/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
