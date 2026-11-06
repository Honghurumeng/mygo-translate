package app

import "testing"

func TestLanguageLabelsRoundTrip(t *testing.T) {
	for _, language := range languages {
		if got := codeOf(labelOf(language.Code)); got != language.Code {
			t.Errorf("code %q came back as %q", language.Code, got)
		}
	}
	if labelOf(autoLanguage) != "自动检测" || codeOf("自动检测") != autoLanguage {
		t.Error("auto detection does not round trip")
	}
}

// 不认识的代码原样显示，不发愁。
func TestUnknownLanguageShowsAsItself(t *testing.T) {
	if got := labelOf("xx"); got != "xx" {
		t.Errorf("labelOf(%q) = %q", "xx", got)
	}
	if got := codeOf("Klingon"); got != "Klingon" {
		t.Errorf("codeOf(%q) = %q", "Klingon", got)
	}
}

func TestGroupDigits(t *testing.T) {
	cases := map[int]string{0: "0", 42: "42", 1234: "1,234", 352498: "352,498", -1234: "-1,234"}
	for n, want := range cases {
		if got := groupDigits(n); got != want {
			t.Errorf("groupDigits(%d) = %q, want %q", n, got, want)
		}
	}
}
