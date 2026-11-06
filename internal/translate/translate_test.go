package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSplitKeepsEveryChunkWithinLimit(t *testing.T) {
	text := strings.Repeat("这是一个用来测试分段的中文句子。", 600)
	chunks := Split(text)

	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want several", len(chunks))
	}
	if joined := strings.Join(chunks, ""); joined != text {
		t.Fatalf("chunks do not reassemble the text")
	}
	for i, chunk := range chunks {
		if n := len([]rune(chunk)); n > ChunkLength {
			t.Errorf("chunk %d holds %d runes, over %d", i, n, ChunkLength)
		}
	}
}

// 句子中间不该被切开：切点应落在「。」之后。
func TestSplitPrefersSentenceEnds(t *testing.T) {
	text := strings.Repeat("a", ChunkLength-2) + "。结尾。后面的内容"
	chunks := Split(text)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if !strings.HasSuffix(chunks[0], "。") {
		t.Errorf("first chunk ends with %q, want a full stop", lastRunes(chunks[0], 6))
	}
}

// 空行是比句号更强的边界。
func TestSplitPrefersBlankLines(t *testing.T) {
	text := strings.Repeat("a", 3000) + "\n\n" + strings.Repeat("b", 3000)
	chunks := Split(text)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if strings.Contains(chunks[0], "b") {
		t.Errorf("first chunk runs past the blank line")
	}
}

// 硬切的位置正好落在 CRLF 中间时，退一格，别把换行拆成两半。
func TestSplitTreatsCRLFAsOneBreak(t *testing.T) {
	text := strings.Repeat("x", ChunkLength-1) + "\r\n" + "tail"
	chunks := Split(text)

	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if !strings.HasPrefix(chunks[1], "\r\n") {
		t.Errorf("second chunk starts with %q, want the whole CRLF", firstRunes(chunks[1], 4))
	}
}

func TestParseGoogleFormats(t *testing.T) {
	cases := []struct {
		name string
		body string
		want Result
	}{
		{
			name: "full",
			body: `[[["你好","hello",null,null,10],["世界","world",null,null,10]],null,"en"]`,
			want: Result{Text: "你好世界", Detected: "en"},
		},
		{
			name: "compact",
			body: `[["你好","en"]]`,
			want: Result{Text: "你好", Detected: "en"},
		},
		{
			name: "object",
			body: `{"sentences":[{"trans":"你好"},{"trans":"世界"}],"src":"en"}`,
			want: Result{Text: "你好世界", Detected: "en"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGoogle([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseGoogleRejectsUnknownPayloads(t *testing.T) {
	if _, err := parseGoogle([]byte(`{"foo":1}`)); err == nil {
		t.Fatal("want an error for an unknown payload")
	}
}

// 长文本按顺序拼回，进度从 1 走到总数。
func TestTranslateKeepsOrderAndReportsProgress(t *testing.T) {
	server := fakeGoogle(t, func(q string) string {
		return "[" + q + "]"
	})
	defer server.Close()
	swapEndpoints(t, server.URL)

	text := strings.Repeat("句子。", 3000)
	var events []Event
	client := &Client{
		HTTP:    server.Client(),
		OnEvent: func(ev Event) { events = append(events, ev) },
	}

	result, err := client.Translate(context.Background(), text, "auto", "en")
	if err != nil {
		t.Fatal(err)
	}

	var want strings.Builder
	for _, chunk := range Split(text) {
		want.WriteString("[" + chunk + "]")
	}
	if result.Text != want.String() {
		t.Fatalf("translation is out of order or incomplete")
	}

	total := len(Split(text))
	if last := events[len(events)-1]; last.Completed != total || last.Total != total {
		t.Errorf("last event %+v, want %d/%d", last, total, total)
	}
}

// 500 是临时故障：退避后重试，最终成功。
func TestTranslateRetriesTransientFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, `[[["ok","x"]]]`)
	}))
	defer server.Close()
	swapEndpoints(t, server.URL)
	swapRetryDelay(t, time.Millisecond)

	var retries []int
	client := &Client{HTTP: server.Client(), OnEvent: func(ev Event) {
		if ev.RetryingChunk > 0 {
			retries = append(retries, ev.RetryAttempt)
		}
	}}

	result, err := client.Translate(context.Background(), "x", "auto", "en")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" {
		t.Errorf("got %q, want %q", result.Text, "ok")
	}
	if len(retries) != 2 || retries[0] != 1 || retries[1] != 2 {
		t.Errorf("retries %v, want [1 2]", retries)
	}
}

// Google 403 时用 MyMemory 兜底。
func TestTranslateFallsBackToMyMemory(t *testing.T) {
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer google.Close()

	mymemory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"responseData":{"translatedText":"兜底"},"responseStatus":200}`)
	}))
	defer mymemory.Close()

	swapEndpoints(t, google.URL)
	swapMyMemory(t, mymemory.URL)

	client := &Client{HTTP: google.Client()}
	result, err := client.Translate(context.Background(), "hello", "en", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "兜底" {
		t.Errorf("got %q, want the MyMemory fallback", result.Text)
	}
}

// 取消后立刻返回，不再重试。
func TestTranslateStopsWhenCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	swapEndpoints(t, server.URL)
	swapRetryDelay(t, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	client := &Client{HTTP: server.Client()}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, err := client.Translate(ctx, "hello", "auto", "zh-CN"); err == nil {
		t.Fatal("want an error after canceling")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Translate took %v after cancel", elapsed)
	}
}

func fakeGoogle(t *testing.T, translate func(q string) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := json.Marshal([]any{[]any{[]any{translate(r.URL.Query().Get("q"))}}})
		w.Write(payload)
	}))
}

func swapEndpoints(t *testing.T, base string) {
	t.Helper()
	original := googleEndpoints
	googleEndpoints = []string{base}
	t.Cleanup(func() { googleEndpoints = original })
}

func swapMyMemory(t *testing.T, base string) {
	t.Helper()
	original := myMemoryEndpoint
	myMemoryEndpoint = base
	t.Cleanup(func() { myMemoryEndpoint = original })
}

func swapRetryDelay(t *testing.T, delay time.Duration) {
	t.Helper()
	original := retryBaseDelay
	retryBaseDelay = delay
	t.Cleanup(func() { retryBaseDelay = original })
}

func lastRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}

func firstRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
