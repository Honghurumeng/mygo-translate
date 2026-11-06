package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-translate/internal/ocr"
	"mygo-translate/internal/translate"
)

// progress 是一次翻译的进度，界面按它显示「正在翻译 x/y 段」。
type progress struct {
	Completed int
	Total     int
	Retrying  int
	Attempt   int
}

// app 是窗口的全部状态。视图每一帧都从它重建界面。
type app struct {
	win *mygo.Window

	// 语言用代码保存，界面上显示 labelOf 给出的名字。
	sourceLang string
	targetLang string

	source   string
	output   string
	detected string
	// chunks 是当前原文会被切成几段，随原文更新，供状态栏显示。
	chunks int

	translating bool
	progress    progress
	errText     string

	// runID 让旧任务的回调认不出自己，cancel 停掉正在进行的请求。
	runID  int
	cancel context.CancelFunc

	// ocrBusy 表示正在识别图片，ocrID 让过期的识别结果作废。
	ocrBusy bool
	ocrID   int

	// toast 是后台任务要提示的一句话，下一帧由视图显示。
	toast string
}

// Run 建窗口并进入事件循环。
func Run() error {
	a := &app{
		sourceLang: autoLanguage,
		targetLang: "zh-CN",
	}

	mygo.App.WhenReady(func() {
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:          "MyGo 翻译",
			Width:          1020,
			Height:         720,
			MinWidth:       760,
			MinHeight:      520,
			StateKey:       "main",
			TitleBarStyle:  mygo.TitleBarHidden,
			TitleBarHeight: 46,
			Content:        ui.View(a.view),
		})
		a.captureIfAsked()
	})

	if err := mygo.App.Run(); err != nil {
		return err
	}
	return nil
}

// startTranslate 在后台翻译原文，进度与结果经窗口回到界面。
func (a *app) startTranslate() {
	text := a.source
	if strings.TrimSpace(text) == "" || a.translating {
		return
	}
	win := a.win
	if win == nil {
		return
	}

	a.cancelTranslation()
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.runID++
	id := a.runID
	source, target := a.sourceLang, a.targetLang

	a.translating = true
	a.errText = ""
	a.output = ""
	a.detected = ""
	a.progress = progress{Total: max(a.chunks, 1)}

	go func() {
		client := &translate.Client{OnEvent: func(ev translate.Event) {
			win.Update(func() {
				if a.runID != id {
					return
				}
				a.progress = progress{
					Completed: ev.Completed,
					Total:     ev.Total,
					Retrying:  ev.RetryingChunk,
					Attempt:   ev.RetryAttempt,
				}
			})
		}}
		result, err := client.Translate(ctx, text, source, target)

		win.Update(func() {
			if a.runID != id {
				return
			}
			a.translating = false
			// 任务结束，把这次请求的 context 也放掉。
			if a.cancel != nil {
				a.cancel()
				a.cancel = nil
			}
			a.progress = progress{}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				a.errText = "翻译失败：" + err.Error()
				return
			}
			a.output = result.Text
			a.detected = result.Detected
		})
	}()
}

// cancelTranslation 停掉翻译，并让在飞的请求认不出自己。
func (a *app) cancelTranslation() {
	a.runID++
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.translating = false
	a.progress = progress{}
}

// clearResult 丢掉译文，原文一改就调用，与前端一致。
func (a *app) clearResult() {
	a.cancelTranslation()
	a.output = ""
	a.detected = ""
	a.errText = ""
}

// clear 清空整页。
func (a *app) clear() {
	a.cancelTranslation()
	a.source = ""
	a.output = ""
	a.detected = ""
	a.errText = ""
	a.chunks = 0
}

// setSource 换掉原文（识别结果、反转都用它），并重算分段数。
func (a *app) setSource(text string) {
	a.cancelTranslation()
	a.source = text
	a.output = ""
	a.detected = ""
	a.errText = ""
	a.chunks = len(translate.Split(text))
}

// recount 在用户改了原文后更新分段数。
func (a *app) recount() {
	a.clearResult()
	a.chunks = len(translate.Split(a.source))
}

// canSwap 判断能否反转语言：自动检测要有检测结果才行。
func (a *app) canSwap() bool {
	return a.sourceLang != autoLanguage || a.detected != ""
}

// swap 反转语言与文本，与前端 reverseTranslation 一致。
func (a *app) swap() {
	from := a.sourceLang
	if from == autoLanguage {
		from = a.detected
	}
	if from == "" {
		return
	}

	a.cancelTranslation()
	a.sourceLang, a.targetLang = a.targetLang, from
	if a.output != "" {
		a.source, a.output = a.output, a.source
		a.chunks = len(translate.Split(a.source))
	}
	a.detected = ""
	a.errText = ""
}

// copyTranslation 把译文放进剪贴板。
func (a *app) copyTranslation() bool {
	if a.output == "" {
		return false
	}
	mygo.Clipboard.WriteText(a.output)
	return true
}

// beginOCR 在后台识别图片文字，结果替换原文。
func (a *app) beginOCR(png []byte, label string) {
	win := a.win
	if win == nil || a.ocrBusy {
		return
	}

	a.ocrBusy = true
	a.ocrID++
	id := a.ocrID

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		text, err := ocr.Recognize(ctx, png)

		win.Update(func() {
			if a.ocrID != id {
				return
			}
			a.ocrBusy = false
			switch {
			case errors.Is(err, ocr.ErrNoText):
				a.toast = label + "中未识别到文字"
			case err != nil:
				a.toast = "识别失败：" + err.Error()
			default:
				a.setSource(text)
				a.toast = fmt.Sprintf("已从%s识别 %d 个字符，可点击翻译", label, utf8.RuneCountInString(text))
			}
		})
	}()
}

// captureIfAsked 是设计检查用的开关：MYGO_TRANSLATE_SHOT=<png> 时
// 截一张界面图，然后退出。
func (a *app) captureIfAsked() {
	path := os.Getenv("MYGO_TRANSLATE_SHOT")
	if path == "" {
		return
	}
	go func() {
		time.Sleep(800 * time.Millisecond)
		mygo.RunOnMain(func() {
			if png, err := a.win.CapturePage(); err == nil {
				os.WriteFile(path, png, 0o644)
			}
			mygo.App.Quit()
		})
	}()
}
