// Package ocr 从图片里认出文字。macOS 用系统的 Vision 框架
// （VNRecognizeTextRequest），经 osascript 的 JavaScript 桥调用，
// 因此不需要 cgo，也不用额外安装什么。
package ocr

import (
	"context"
	"errors"
)

// ErrNoText 表示图片里没有可识别的文字。
var ErrNoText = errors.New("未能从图片中识别出文字")

// Supported 报告当前平台能否识别图片文字。
func Supported() bool {
	return supported
}

// Recognize 识别 PNG 图片中的文字，按行用换行符连接。
func Recognize(ctx context.Context, png []byte) (string, error) {
	if len(png) == 0 {
		return "", errors.New("图片数据为空")
	}
	if !supported {
		return "", errors.New("图片文字识别目前仅支持 macOS")
	}
	return recognize(ctx, png)
}
