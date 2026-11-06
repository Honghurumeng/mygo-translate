//go:build !darwin

package ocr

import (
	"context"
	"errors"
)

const supported = false

func recognize(ctx context.Context, png []byte) (string, error) {
	return "", errors.New("图片文字识别目前仅支持 macOS")
}
