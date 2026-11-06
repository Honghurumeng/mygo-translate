// Package translate 实现文本翻译：把长文本按句子切成若干段，并发请求
// Google 的免费接口，失败时退回 MyMemory，并对可重试的错误按指数退避重试。
//
// 这套流程与 Codex Switch（src/App.tsx）里的翻译弹窗一致，只是搬到了 Go：
//
//   - 每段最多 4500 个字符，优先在空行、句末标点、换行与空格处切开；
//   - 同时最多 2 段在飞，失败最多重试 3 次（1s、2s、4s）；
//   - Google 的两个端点依次尝试，仍失败且错误可重试时用 MyMemory 兜底；
//   - Auto 检测到的语言按各段长度的权重投票，票多者为准。
package translate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// ChunkLength 是单个请求的最大字符数（按 rune 计）。
	ChunkLength = 4500
	// minChunkRatio 决定切分点最晚可以落在哪里：段长的 60% 之后才算数，
	// 否则宁可切得晚一点。
	minChunkRatio = 0.6
	// MyMemoryChunkLength 是 MyMemory 一次能吃下的字符数，硬上限 500。
	MyMemoryChunkLength = 450
	// Concurrency 是同时进行的请求数。
	Concurrency = 2
	// MaxRetries 是单段失败后的自动重试次数。
	MaxRetries = 3
)

// retryBaseDelay 是指数退避的基数：1s、2s、4s。测试里会缩短它。
var retryBaseDelay = time.Second

// Google 的两个镜像端点，顺序与前端一致。
var googleEndpoints = []string{
	"https://clients5.google.com/translate_a/single",
	"https://translate.googleapis.com/translate_a/single",
}

// MyMemory 的兜底端点。
var myMemoryEndpoint = "https://api.mymemory.translated.net/get"

// 切分点候选，从「最像段落边界」到「最不像」依次尝试。
var (
	blankLineRe = regexp.MustCompile(`\r?\n[ \t]*\r?\n`)
	sentenceRes = []*regexp.Regexp{
		regexp.MustCompile(`[。！？!?；;]+[”’"'）)\]}】》」』]*[ \t]*`),
		regexp.MustCompile(`\.+[”’"'）)\]}】》」』]*(?:[ \t]+|$)`),
	}
	newlineRe    = regexp.MustCompile(`\r?\n`)
	whitespaceRe = regexp.MustCompile(`[ \t]+`)
)

// Result 是一段或整篇的翻译结果。
type Result struct {
	// Text 是译文。
	Text string
	// Detected 是接口报告的原语言，检测不到时为空。
	Detected string
}

// Event 报告进度：已完成的段数，或第几段正在重试。
type Event struct {
	Completed int
	Total     int
	// RetryingChunk 是正在重试的段号（从 1 开始），0 表示只是在推进度。
	RetryingChunk int
	// RetryAttempt 是这次重试的序号（从 1 开始）。
	RetryAttempt int
}

// Client 是一次翻译任务要用的 HTTP 客户端。零值可用。
type Client struct {
	// HTTP 为空时用带超时的默认客户端。
	HTTP *http.Client
	// OnEvent 在每段完成或重试时调用，可能来自任意 goroutine。
	OnEvent func(Event)
}

// HTTPError 是接口返回的非 200 状态码。
type HTTPError struct {
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("请求失败（HTTP %d）", e.Status)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

var defaultHTTP = &http.Client{Timeout: 45 * time.Second}

func (c *Client) report(ev Event) {
	if c.OnEvent != nil {
		c.OnEvent(ev)
	}
}

// Translate 把 text 从 source 译到 target，source 可以是 "auto"。
func (c *Client) Translate(ctx context.Context, text, source, target string) (Result, error) {
	chunks := Split(text)
	if len(chunks) == 0 {
		return Result{}, errors.New("没有需要翻译的内容")
	}

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]Result, len(chunks))
	var (
		mu        sync.Mutex
		next      int
		completed int
		firstErr  error
		weights   = make(map[string]int)
		order     []string
	)

	fail := func(index int, err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = fmt.Errorf("第 %d/%d 段处理失败：%w", index+1, len(chunks), err)
		}
		mu.Unlock()
		cancel()
	}

	workers := min(Concurrency, len(chunks))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				if firstErr != nil || next >= len(chunks) {
					mu.Unlock()
					return
				}
				index := next
				next++
				mu.Unlock()

				result, err := c.translateChunk(ctx, chunks[index], source, target, func(attempt int) {
					mu.Lock()
					done := completed
					mu.Unlock()
					c.report(Event{
						Completed:     done,
						Total:         len(chunks),
						RetryingChunk: index + 1,
						RetryAttempt:  attempt,
					})
				})
				if err != nil {
					fail(index, err)
					return
				}

				mu.Lock()
				results[index] = result
				completed++
				done := completed
				if result.Detected != "" {
					language := result.Detected
					if _, seen := weights[language]; !seen {
						order = append(order, language)
					}
					weights[language] += utf8.RuneCountInString(strings.TrimSpace(chunks[index]))
				}
				mu.Unlock()

				c.report(Event{Completed: done, Total: len(chunks)})
			}
		}()
	}
	wg.Wait()

	if err := parent.Err(); err != nil {
		return Result{}, err
	}
	if firstErr != nil {
		return Result{}, firstErr
	}

	var out strings.Builder
	for _, result := range results {
		out.WriteString(result.Text)
	}

	detected := ""
	best := -1
	for _, language := range order {
		if weights[language] > best {
			detected, best = language, weights[language]
		}
	}

	return Result{Text: out.String(), Detected: detected}, nil
}

// translateChunk 翻译一段，保留它首尾的空白，并在可重试的错误上退避重试。
func (c *Client) translateChunk(ctx context.Context, chunk, source, target string, onRetry func(attempt int)) (Result, error) {
	leading, content, trailing := splitOuterSpace(chunk)
	if content == "" {
		return Result{Text: chunk}, nil
	}

	for attempt := 0; ; attempt++ {
		result, err := c.request(ctx, content, source, target)
		if err == nil {
			return Result{Text: leading + result.Text + trailing, Detected: result.Detected}, nil
		}
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if !isRetryable(err) || attempt >= MaxRetries {
			if attempt > 0 {
				return Result{}, fmt.Errorf("%w（已自动重试 %d 次）", err, attempt)
			}
			return Result{}, err
		}

		onRetry(attempt + 1)
		if err := sleep(ctx, retryBaseDelay<<attempt); err != nil {
			return Result{}, err
		}
	}
}

// request 先问 Google，可重试的失败再交给 MyMemory。
func (c *Client) request(ctx context.Context, text, source, target string) (Result, error) {
	result, err := c.requestGoogle(ctx, text, source, target)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil || !isRetryable(err) {
		return Result{}, err
	}

	fallback, fallbackErr := c.requestMyMemory(ctx, text, source, target)
	if fallbackErr == nil {
		return fallback, nil
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	// 兜底也失败时，报 Google 的错，与前端一致。
	return Result{}, err
}

func (c *Client) requestGoogle(ctx context.Context, text, source, target string) (Result, error) {
	var lastErr error
	for _, endpoint := range googleEndpoints {
		query := url.Values{
			"client": {"gtx"},
			"dt":     {"t"},
			"sl":     {source},
			"tl":     {target},
			"q":      {text},
		}
		body, err := c.get(ctx, endpoint+"?"+query.Encode())
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			var httpErr *HTTPError
			if errors.As(err, &httpErr) && !isRetryableStatus(httpErr.Status) {
				return Result{}, err
			}
			continue
		}

		result, err := parseGoogle(body)
		if err != nil {
			return Result{}, err
		}
		return result, nil
	}
	if lastErr == nil {
		lastErr = errors.New("Google 翻译暂不可用")
	}
	return Result{}, lastErr
}

func (c *Client) requestMyMemory(ctx context.Context, text, source, target string) (Result, error) {
	var out strings.Builder
	for _, part := range splitMyMemory(text) {
		query := url.Values{
			"q":        {part},
			"langpair": {guessMyMemorySource(part, source) + "|" + normalizeMyMemoryLang(target)},
		}
		body, err := c.get(ctx, myMemoryEndpoint+"?"+query.Encode())
		if err != nil {
			return Result{}, err
		}

		var payload struct {
			ResponseData struct {
				TranslatedText string `json:"translatedText"`
			} `json:"responseData"`
			ResponseDetails string `json:"responseDetails"`
			ResponseStatus  int    `json:"responseStatus"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return Result{}, fmt.Errorf("翻译接口返回了无法识别的数据：%w", err)
		}
		if strings.Contains(payload.ResponseData.TranslatedText, "QUERY LENGTH LIMIT EXCEEDED") {
			return Result{}, errors.New("MyMemory 单次限 500 字符")
		}
		if payload.ResponseStatus != 0 && payload.ResponseStatus != 200 {
			if strings.Contains(payload.ResponseDetails, "INVALID SOURCE LANGUAGE") {
				return Result{}, errors.New("MyMemory 不支持 auto，Google 主接口暂不可用")
			}
			if payload.ResponseDetails != "" {
				return Result{}, errors.New(payload.ResponseDetails)
			}
			return Result{}, fmt.Errorf("MyMemory 请求失败（%d）", payload.ResponseStatus)
		}
		if payload.ResponseData.TranslatedText == "" {
			return Result{}, errors.New("翻译接口返回了无法识别的数据")
		}
		out.WriteString(payload.ResponseData.TranslatedText)
	}
	return Result{Text: out.String()}, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "mygo-translate/0.1")
	request.Header.Set("Accept", "application/json,text/plain,*/*")

	response, err := c.httpClient().Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return nil, &HTTPError{Status: response.StatusCode}
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

// parseGoogle 认得 Google 返回的几种形状：
//
//	[[["你好","hello",...],["世界","world",...]], null, "en"]   完整格式
//	[["你好","en"]]                                            clients5 的精简格式
//	{"sentences":[{"trans":"你好"}],"src":"en"}                对象格式
func parseGoogle(body []byte) (Result, error) {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return Result{}, fmt.Errorf("翻译接口返回了无法识别的数据：%w", err)
	}

	switch value := payload.(type) {
	case []any:
		if len(value) == 0 {
			break
		}
		first, ok := value[0].([]any)
		if !ok || len(first) == 0 {
			break
		}

		// 完整格式：每个元素是 [译文, 原文, ...]。
		if _, nested := first[0].([]any); nested {
			var out strings.Builder
			for _, segment := range first {
				if pair, ok := segment.([]any); ok && len(pair) > 0 {
					if text, ok := pair[0].(string); ok {
						out.WriteString(text)
					}
				}
			}
			if out.Len() > 0 {
				return Result{Text: out.String(), Detected: stringAt(value, 2)}, nil
			}
			break
		}

		// 精简格式：[["你好","en"]]。
		if text, ok := first[0].(string); ok && text != "" {
			detected := ""
			if len(first) > 1 {
				detected, _ = first[1].(string)
			}
			if detected == "" {
				detected = stringAt(value, 2)
			}
			return Result{Text: text, Detected: detected}, nil
		}

	case map[string]any:
		if sentences, ok := value["sentences"].([]any); ok {
			var out strings.Builder
			for _, item := range sentences {
				if sentence, ok := item.(map[string]any); ok {
					if text, ok := sentence["trans"].(string); ok {
						out.WriteString(text)
					}
				}
			}
			if out.Len() > 0 {
				detected, _ := value["src"].(string)
				return Result{Text: out.String(), Detected: detected}, nil
			}
		}
		if data, ok := value["responseData"].(map[string]any); ok {
			text, _ := data["translatedText"].(string)
			if strings.Contains(text, "QUERY LENGTH LIMIT EXCEEDED") {
				return Result{}, errors.New("MyMemory 单次限 500 字符，请减小分片")
			}
			if text != "" {
				if status, ok := value["responseStatus"].(float64); ok && status != 200 {
					break
				}
				return Result{Text: text}, nil
			}
		}
		if details, _ := value["responseDetails"].(string); strings.Contains(details, "INVALID SOURCE LANGUAGE") {
			return Result{}, errors.New("MyMemory 不支持 auto，Google 主接口暂不可用")
		}
	}

	return Result{}, errors.New("翻译接口返回了无法识别的数据")
}

func stringAt(values []any, index int) string {
	if index < len(values) {
		text, _ := values[index].(string)
		return text
	}
	return ""
}

func isRetryable(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return isRetryableStatus(httpErr.Status)
	}
	// 网络错误、解析错误都值得再试一次；取消由调用方判断。
	return !errors.Is(err, context.Canceled)
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Split 把文本切成若干段，每段不超过 ChunkLength 个字符。
func Split(text string) []string {
	runes := []rune(text)
	var chunks []string
	for len(runes) > 0 {
		size := nextChunkLength(runes)
		chunks = append(chunks, string(runes[:size]))
		runes = runes[size:]
	}
	return chunks
}

func nextChunkLength(runes []rune) int {
	if len(runes) <= ChunkLength {
		return len(runes)
	}

	candidate := string(runes[:ChunkLength])
	minimum := int(float64(ChunkLength) * minChunkRatio)

	for _, patterns := range [][]*regexp.Regexp{
		{blankLineRe},
		sentenceRes,
		{newlineRe},
		{whitespaceRe},
	} {
		if boundary := lastBoundary(candidate, patterns, minimum); boundary > 0 {
			return boundary
		}
	}
	return hardBoundary(runes)
}

// lastBoundary 找出所有匹配里终点最靠后的一个，换算成 rune 下标。
func lastBoundary(text string, patterns []*regexp.Regexp, minimum int) int {
	boundary := -1
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringIndex(text, -1) {
			end := utf8.RuneCountInString(text[:match[1]])
			if end >= minimum && end > boundary {
				boundary = end
			}
		}
	}
	return boundary
}

// hardBoundary 在没有合适切分点时硬切，只避开 CRLF 中间。
func hardBoundary(runes []rune) int {
	boundary := min(ChunkLength, len(runes))
	if boundary > 0 && boundary < len(runes) && runes[boundary-1] == '\r' && runes[boundary] == '\n' {
		boundary--
	}
	return boundary
}

// splitMyMemory 把一段切成 MyMemory 能接受的长度，不拆开 CRLF。
func splitMyMemory(text string) []string {
	runes := []rune(text)
	var parts []string
	for len(runes) > 0 {
		size := min(MyMemoryChunkLength, len(runes))
		if size < len(runes) && runes[size-1] == '\r' && runes[size] == '\n' {
			size--
		}
		parts = append(parts, string(runes[:size]))
		runes = runes[size:]
	}
	return parts
}

func splitOuterSpace(text string) (leading, content, trailing string) {
	trimmedStart := strings.TrimLeftFunc(text, unicode.IsSpace)
	leading = text[:len(text)-len(trimmedStart)]
	content = strings.TrimRightFunc(trimmedStart, unicode.IsSpace)
	trailing = trimmedStart[len(content):]
	return leading, content, trailing
}

func normalizeMyMemoryLang(language string) string {
	if language == "auto" {
		return "en"
	}
	return language
}

// guessMyMemorySource 在没有指定源语言时按文字猜一个，MyMemory 不接受 auto。
func guessMyMemorySource(text, fallback string) string {
	if fallback != "" && fallback != "auto" {
		return normalizeMyMemoryLang(fallback)
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			return "zh-CN"
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			return "ja"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		}
	}
	return "en"
}
