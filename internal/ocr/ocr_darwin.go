//go:build darwin

package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const supported = true

// visionScript 用 JXA 的 ObjC 桥调用 Vision：读图、跑一次
// VNRecognizeTextRequest、把每一行的首选结果收集起来，以 JSON 输出。
// 语言列表与 Codex Switch 的 Rust 实现保持一致，按优先级排列。
const visionScript = `ObjC.import('Foundation');
ObjC.import('Vision');

function run(argv) {
  const path = argv[0];
  const data = $.NSData.dataWithContentsOfFile($(path));
  if (!data) return JSON.stringify({ error: '读取图片失败' });

  const handler = $.VNImageRequestHandler.alloc.initWithDataOptions(data, $({}));
  const request = $.VNRecognizeTextRequest.alloc.init;
  request.recognitionLevel = 0;
  request.usesLanguageCorrection = true;
  request.recognitionLanguages = $(['zh-Hans', 'zh-Hant', 'en-US', 'ja-JP', 'ko-KR',
    'fr-FR', 'de-DE', 'es-ES', 'pt-BR', 'ru-RU', 'it-IT', 'nl-NL', 'pl-PL', 'tr-TR']);

  if (!handler.performRequestsError($([request]), null)) {
    return JSON.stringify({ error: '图像识别失败' });
  }

  const results = request.results;
  const lines = [];
  if (results) {
    for (let i = 0; i < results.count; i++) {
      const candidates = results.objectAtIndex(i).topCandidates(1);
      if (candidates.count > 0) {
        const text = ObjC.unwrap(candidates.objectAtIndex(0).string);
        if (text && text.trim()) lines.push(text);
      }
    }
  }
  return JSON.stringify({ lines: lines });
}`

// recognize 把 PNG 写到临时文件，交给 osascript 里的 Vision 处理。
func recognize(ctx context.Context, png []byte) (string, error) {
	dir, err := os.MkdirTemp("", "mygo-translate-ocr")
	if err != nil {
		return "", fmt.Errorf("准备临时目录失败：%w", err)
	}
	defer os.RemoveAll(dir)

	imagePath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(imagePath, png, 0o600); err != nil {
		return "", fmt.Errorf("写入临时图片失败：%w", err)
	}
	scriptPath := filepath.Join(dir, "ocr.js")
	if err := os.WriteFile(scriptPath, []byte(visionScript), 0o600); err != nil {
		return "", fmt.Errorf("写入识别脚本失败：%w", err)
	}

	command := exec.CommandContext(ctx, "osascript", "-l", "JavaScript", scriptPath, imagePath)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", fmt.Errorf("图像识别失败：%s", detail)
	}

	var payload struct {
		Lines []string `json:"lines"`
		Error string   `json:"error"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return "", fmt.Errorf("识别结果解析失败：%w", err)
	}
	if payload.Error != "" {
		return "", fmt.Errorf("图像识别失败：%s", payload.Error)
	}
	text := strings.TrimSpace(strings.Join(payload.Lines, "\n"))
	if text == "" {
		return "", ErrNoText
	}
	return text, nil
}
