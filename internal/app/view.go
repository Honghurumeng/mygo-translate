package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-translate/internal/ocr"
	"mygo-translate/internal/translate"
)

// view 由状态搭出整页：标题、语言栏、双栏编辑区、底部状态与按钮。
func (a *app) view(c *ui.Context) {
	t := c.Theme()

	// ⌘↩ 翻译，Esc 取消，和弹窗里的习惯一致。
	if c.Shortcut(ui.Cmd, ui.KeyEnter) {
		a.startTranslate()
	}
	if c.Shortcut(0, ui.KeyEscape) {
		a.cancelTranslation()
	}
	// 后台任务留下的提示，在这里补出来。
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}

	bar := c.TitleBar()
	headerHeight := max(bar.Height, 30) + 24

	ui.Column(c).Fill().Background(t.Background).Children(func() {
		// 隐藏了系统标题栏，这一条自己画，空白处拖动窗口。
		ui.Row(c).Height(headerHeight).Gap(10).
			Padding(0, 20, 0, bar.Left+20).
			AlignItems(ui.Center).DragWindow().Children(func() {
			a.mark(c)
			ui.Text(c, "文本翻译").FontSize(15).Bold()
			ui.Text(c, "长文本自动分段 · 失败自动重试 · ⌘↩ 翻译").
				FontSize(12).TextColor(t.TextMuted)
		})

		ui.Column(c).Fill().Padding(20, 20, 16, 20).Gap(14).Children(func() {
			a.toolbar(c)
			a.editors(c)
			a.footer(c)
		})
	})
}

// mark 是标题左边的方形标记，没有图片资源时用它当图标。
func (a *app) mark(c *ui.Context) {
	t := c.Theme()
	ui.Box(c).Size(28, 28).Radius(8).Background(t.Accent).Center().Children(func() {
		ui.Text(c, "译").FontSize(15).Bold().TextColor(t.AccentText)
	})
}

// toolbar 是语言选择、反转与图片识别。
func (a *app) toolbar(c *ui.Context) {
	t := c.Theme()
	ui.Row(c).Gap(10).AlignItems(ui.End).Children(func() {
		a.languageSelect(c, "原始语言", sourceLabels(), a.sourceLang, func(label string) {
			a.sourceLang = codeOf(label)
			a.clearResult()
		})

		swap := ui.Button(c, "⇄").Label("反转语言和文本").Width(42).Height(30).
			Disabled(!a.canSwap())
		if swap.Clicked() {
			a.swap()
		}

		a.languageSelect(c, "目标语言", targetLabels(), a.targetLang, func(label string) {
			a.targetLang = codeOf(label)
			a.clearResult()
		})

		ui.Spacer(c)

		if !ocr.Supported() {
			ui.Text(c, "图片识别仅 macOS 可用").FontSize(12).TextColor(t.TextMuted)
		}
		label := "粘贴图片识别"
		if a.ocrBusy {
			label = "识别中…"
		}
		if ui.Button(c, label).Disabled(a.ocrBusy || !ocr.Supported()).Clicked() {
			a.recognizeClipboard(c)
		}
	})
}

// languageSelect 是「标题在上、下拉在下」的语言选择。
func (a *app) languageSelect(c *ui.Context, title string, options []string, code string, apply func(string)) {
	t := c.Theme()
	value := labelOf(code)
	ui.Column(c).Gap(6).Width(184).Children(func() {
		ui.Text(c, title).FontSize(12).TextColor(t.TextMuted)
		if ui.Select(c, &value, options).Label(title).Changed() {
			apply(value)
		}
	})
}

// editors 左右并排放原文与译文。
func (a *app) editors(c *ui.Context) {
	// Stretch 让两张卡片跟着行高长满，文本域才有整页的高度可用。
	ui.Row(c).Grow(1).Gap(14).AlignItems(ui.Stretch).Children(func() {
		a.sourceCard(c)
		a.outputCard(c)
	})
}

func (a *app) sourceCard(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(14).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "原文").Bold()
			if a.sourceLang == autoLanguage && a.detected != "" {
				ui.Text(c, "检测为 "+labelOf(a.detected)).FontSize(11).
					TextColor(t.Accent).Padding(3, 8).Radius(6).Background(t.Selection)
			}
			ui.Spacer(c)
			if ui.Button(c, "清空").Disabled(a.source == "" && a.output == "").Clicked() {
				a.clear()
			}
		})

		area := ui.TextArea(c, &a.source).Grow(1).Lines(4, 8).
			Background(t.Background).Border(1, t.Border).Radius(8).
			Placeholder("输入需要翻译的内容，或直接 ⌘V 粘贴图片自动识别").
			Label("需要翻译的原文").
			OnPaste(func(text string) bool {
				// 剪贴板里是图片时，拦下这次粘贴，改去识别文字。
				if text != "" {
					return false
				}
				png := mygo.Clipboard.ReadImage()
				if len(png) == 0 {
					return false
				}
				a.beginOCR(png, "粘贴的图片")
				return true
			})
		if area.Changed() {
			a.recount()
		}
	})
}

func (a *app) outputCard(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Padding(14).Gap(10).Radius(12).
		Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "译文").Bold()
			ui.Spacer(c)
			if ui.Button(c, "复制").Disabled(a.output == "").Clicked() {
				if a.copyTranslation() {
					c.Toast("译文已复制")
				}
			}
		})

		placeholder := "翻译结果将显示在这里"
		if a.translating {
			placeholder = "正在翻译…"
		}
		ui.TextArea(c, &a.output).ReadOnly(true).Grow(1).Lines(4, 8).
			Background(t.Background).Border(1, t.Border).Radius(8).
			Placeholder(placeholder).Label("翻译结果")
	})
}

// footer 是进度、状态说明与「翻译」按钮。
func (a *app) footer(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Gap(10).Children(func() {
		if a.translating && a.progress.Total > 0 {
			ratio := float32(a.progress.Completed) / float32(a.progress.Total)
			ui.Box(c).Height(3).Radius(2).AlignItems(ui.Start).
				Background(t.Border).Children(func() {
				ui.Box(c).Height(3).Radius(2).WidthPercent(ratio * 100).Background(t.Accent)
			})
		}

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			status, color := a.status(c)
			ui.Text(c, status).FontSize(12).TextColor(color).Grow(1)

			if a.translating {
				if ui.Button(c, "取消").Clicked() {
					a.cancelTranslation()
				}
			}

			label := "翻译"
			if a.translating {
				label = "翻译中…"
			}
			if ui.PrimaryButton(c, label).
				Disabled(a.translating || strings.TrimSpace(a.source) == "").Clicked() {
				a.startTranslate()
			}
		})
	})
}

// status 是底部那行说明：错误、进度，或分段提示。
func (a *app) status(c *ui.Context) (string, ui.Color) {
	t := c.Theme()
	switch {
	case a.errText != "":
		return a.errText, t.Danger
	case a.translating && a.progress.Retrying > 0:
		return fmt.Sprintf("第 %d/%d 段请求失败，准备自动重试（%d/%d）",
			a.progress.Retrying, a.progress.Total, a.progress.Attempt, translate.MaxRetries), t.Warning
	case a.translating:
		return fmt.Sprintf("正在翻译 %d/%d 段", a.progress.Completed, a.progress.Total), t.TextMuted
	case strings.TrimSpace(a.source) != "":
		count := utf8.RuneCountInString(a.source)
		return fmt.Sprintf("%s 个字符，将自动分为 %d 段", groupDigits(count), max(a.chunks, 1)), t.TextMuted
	}
	return fmt.Sprintf("长文本按每段不超过 %d 字符自动分段，失败最多自动重试 %d 次",
		translate.ChunkLength, translate.MaxRetries), t.TextMuted
}

// recognizeClipboard 读剪贴板里的图片并识别文字。
func (a *app) recognizeClipboard(c *ui.Context) {
	png := mygo.Clipboard.ReadImage()
	if len(png) == 0 {
		c.Toast("剪贴板中没有图片，请先截图（⌃⌘⇧4）或复制图片")
		return
	}
	a.beginOCR(png, "剪贴板图片")
}
