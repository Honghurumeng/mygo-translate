package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTypingShowsCharacterAndChunkCount(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1020, 720)

	if err := tt.Click("需要翻译的原文"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hello world")

	if a.source != "hello world" {
		t.Fatalf("source %q after typing", a.source)
	}
	if a.chunks != 1 {
		t.Errorf("chunks %d, want 1", a.chunks)
	}
	if !tt.HasText("11 个字符，将自动分为 1 段") {
		t.Errorf("status texts %q", tt.Texts())
	}
}

func TestClearEmptiesBothPanes(t *testing.T) {
	a := newTestApp()
	a.source = "hello"
	a.output = "你好"
	a.chunks = 1
	tt := ui.NewTester(a.view, 1020, 720)

	if err := tt.Click("清空"); err != nil {
		t.Fatal(err)
	}
	if a.source != "" || a.output != "" || a.chunks != 0 {
		t.Fatalf("after clearing: source %q, output %q, chunks %d", a.source, a.output, a.chunks)
	}
}

// 反转语言要把语言和两段文本一起换过来。
func TestSwapExchangesLanguagesAndText(t *testing.T) {
	a := newTestApp()
	a.source = "hello"
	a.output = "你好"
	a.detected = "en"
	tt := ui.NewTester(a.view, 1020, 720)

	if err := tt.Click("反转语言和文本"); err != nil {
		t.Fatal(err)
	}
	if a.sourceLang != "zh-CN" || a.targetLang != "en" {
		t.Errorf("languages %q -> %q, want zh-CN -> en", a.sourceLang, a.targetLang)
	}
	if a.source != "你好" || a.output != "hello" {
		t.Errorf("texts %q / %q, want them swapped", a.source, a.output)
	}
	if a.detected != "" {
		t.Errorf("detected %q, want it cleared", a.detected)
	}
}

// 自动检测还没检测出结果时，反转按钮是灰的，点了也不会换。
func TestSwapIsDisabledWithoutDetection(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1020, 720)

	_ = tt.Click("反转语言和文本")
	if a.sourceLang != autoLanguage || a.targetLang != "zh-CN" {
		t.Errorf("languages %q -> %q after a disabled click", a.sourceLang, a.targetLang)
	}
}

func newTestApp() *app {
	return &app{sourceLang: autoLanguage, targetLang: "zh-CN"}
}
