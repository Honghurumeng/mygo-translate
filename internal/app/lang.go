package app

import (
	"strconv"
	"strings"
)

// language 是语言选择里的一个选项：接口用的代码与界面上的名字。
type language struct {
	Code  string
	Label string
}

// autoLanguage 是「自动检测」的代码，与前端一致。
const autoLanguage = "auto"

// languages 与 Codex Switch 的 TRANSLATION_LANGUAGE_OPTIONS 一一对应。
var languages = []language{
	{"zh-CN", "简体中文"},
	{"zh-TW", "繁体中文"},
	{"en", "英语"},
	{"ja", "日语"},
	{"ko", "韩语"},
	{"fr", "法语"},
	{"de", "德语"},
	{"es", "西班牙语"},
	{"pt", "葡萄牙语"},
	{"ru", "俄语"},
	{"ar", "阿拉伯语"},
	{"hi", "印地语"},
	{"th", "泰语"},
	{"vi", "越南语"},
	{"id", "印度尼西亚语"},
	{"ms", "马来语"},
	{"it", "意大利语"},
	{"nl", "荷兰语"},
	{"pl", "波兰语"},
	{"tr", "土耳其语"},
	{"uk", "乌克兰语"},
	{"cs", "捷克语"},
	{"da", "丹麦语"},
	{"fi", "芬兰语"},
	{"sv", "瑞典语"},
	{"no", "挪威语"},
	{"ro", "罗马尼亚语"},
	{"hu", "匈牙利语"},
	{"el", "希腊语"},
	{"he", "希伯来语"},
	{"fa", "波斯语"},
	{"ur", "乌尔都语"},
	{"bn", "孟加拉语"},
}

// sourceLabels 是原始语言的选项，多一个「自动检测」。
func sourceLabels() []string {
	labels := make([]string, 0, len(languages)+1)
	labels = append(labels, "自动检测")
	for _, item := range languages {
		labels = append(labels, item.Label)
	}
	return labels
}

// targetLabels 是目标语言的选项。
func targetLabels() []string {
	labels := make([]string, 0, len(languages))
	for _, item := range languages {
		labels = append(labels, item.Label)
	}
	return labels
}

// labelOf 把语言代码换成界面上的名字，不认识的代码原样返回。
func labelOf(code string) string {
	if code == "" || code == autoLanguage {
		return "自动检测"
	}
	for _, item := range languages {
		if item.Code == code {
			return item.Label
		}
	}
	return code
}

// codeOf 把界面上的名字换回语言代码。
func codeOf(label string) string {
	if label == "自动检测" {
		return autoLanguage
	}
	for _, item := range languages {
		if item.Label == label {
			return item.Code
		}
	}
	return label
}

// groupDigits 给数字加千位分隔，与前端 toLocaleString 的观感一致。
func groupDigits(n int) string {
	digits := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var out strings.Builder
	out.WriteString(sign)
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return out.String()
}
