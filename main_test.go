package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "test-token")
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("HEALTH_ADDR", "")
	t.Setenv("MAX_CONCURRENT_DOWNLOADS", "")
	cfg, err := loadConfig()
	if err != nil || cfg.workers != 2 || cfg.healthAddress != ":8080" {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	for _, value := range []string{"0", "-1", "17", "bad"} {
		t.Setenv("MAX_CONCURRENT_DOWNLOADS", value)
		if _, err := loadConfig(); err == nil {
			t.Fatalf("accepted worker count %q", value)
		}
	}
	t.Setenv("MAX_CONCURRENT_DOWNLOADS", "3")
	t.Setenv("HEALTH_ADDR", "127.0.0.1:9090")
	cfg, err = loadConfig()
	if err != nil || cfg.workers != 3 || cfg.healthAddress != "127.0.0.1:9090" {
		t.Fatalf("overrides: %+v, %v", cfg, err)
	}
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("missing token was accepted")
	}
}

func TestHealthHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	handler := healthHandler(ctx)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, resp.Code)
		}
	}
	cancel()
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown readiness returned %d", resp.Code)
	}
}

func TestTelegramClientCancelsAndRedacts(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	client := telegramClient{ctx: ctx, client: &http.Client{Timeout: time.Second}}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1/bot-private-token/getUpdates", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if resp != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("want a redacted request error, got %v", err)
	}
}

func TestProcessMediaMessage(t *testing.T) {
	for _, raw := range []string{"https://instagram.com/reel/abc/", "https://x.com/user/status/123", "https://twitter.com/user/status/123"} {
		t.Run(raw, func(t *testing.T) {
			d, root, _ := fakeDownloader(t, "success")
			var uploaded, deleted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/getMe"):
					_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"testbot"}}`)
				case strings.HasSuffix(r.URL.Path, "/sendVideo"):
					if err := r.ParseMultipartForm(1024 * 1024); err != nil {
						t.Error(err)
						http.Error(w, "invalid upload", http.StatusBadRequest)
						return
					}
					defer func() {
						if err := r.MultipartForm.RemoveAll(); err != nil {
							t.Error(err)
						}
					}()
					file, _, err := r.FormFile("video")
					if err != nil {
						t.Error(err)
						http.Error(w, "missing video", http.StatusBadRequest)
						return
					}
					data, err := io.ReadAll(file)
					if closeErr := file.Close(); closeErr != nil {
						t.Error(closeErr)
					}
					if err != nil || string(data) != "fake video" || r.FormValue("chat_id") != "42" {
						t.Errorf("invalid upload: %q, %v", data, err)
					}
					uploaded.Store(true)
					_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":2}}`)
				case strings.HasSuffix(r.URL.Path, "/deleteMessage"):
					deleted.Store(true)
					_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
					_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				default:
					_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
				}
			}))
			defer server.Close()
			bot, err := tgbotapi.NewBotAPIWithAPIEndpoint("test-token", server.URL+"/bot%s/%s")
			if err != nil {
				t.Fatal(err)
			}
			msg := &tgbotapi.Message{MessageID: 7, Chat: &tgbotapi.Chat{ID: 42}}
			processMediaMessage(t.Context(), bot, d, msg, raw)
			if !uploaded.Load() || !deleted.Load() {
				t.Fatalf("upload=%v, status deleted=%v", uploaded.Load(), deleted.Load())
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary files remain after upload: %v, %v", entries, err)
			}
		})
	}
}
