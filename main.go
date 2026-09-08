package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var version = "dev"

type config struct {
	token         string
	healthAddress string
	workers       int
}

func loadConfig() (config, error) {
	cfg := config{token: os.Getenv("TELEGRAM_BOT_TOKEN"), healthAddress: ":8080", workers: 2}
	if cfg.token == "" {
		cfg.token = os.Getenv("BOT_TOKEN")
	}
	if cfg.token == "" {
		return cfg, errors.New("TELEGRAM_BOT_TOKEN is required")
	}
	if value := os.Getenv("HEALTH_ADDR"); value != "" {
		cfg.healthAddress = value
	}
	if value := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); value != "" {
		workers, err := strconv.Atoi(value)
		if err != nil || workers < 1 || workers > 16 {
			return cfg, errors.New("MAX_CONCURRENT_DOWNLOADS must be an integer from 1 to 16")
		}
		cfg.workers = workers
	}
	return cfg, nil
}

func main() {
	showVersion := flag.Bool("version", false, "print the build version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

// telegramClient gives uploads and long polls the process cancellation context.
type telegramClient struct {
	ctx    context.Context
	client *http.Client
}

func (c telegramClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req.WithContext(c.ctx))
	if err != nil {
		return nil, errors.New("telegram HTTP request failed")
	}
	return resp, nil
}

func run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	downloader, err := NewDownloader()
	if err != nil {
		return err
	}
	bot, err := tgbotapi.NewBotAPIWithClient(cfg.token, tgbotapi.APIEndpoint, telegramClient{
		ctx: ctx, client: &http.Client{Timeout: 75 * time.Second},
	})
	if err != nil {
		// Telegram request errors can include the token in the URL.
		return errors.New("connect to Telegram failed; check the token and network access")
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.healthAddress)
	if err != nil {
		return fmt.Errorf("start health server: %w", err)
	}
	server := &http.Server{Handler: healthHandler(ctx), ReadHeaderTimeout: 5 * time.Second}
	defer func() {
		if err := server.Close(); err != nil {
			log.Printf("Close health server: %v", err)
		}
	}()
	healthErrors := make(chan error, 1)
	go func() { healthErrors <- server.Serve(listener) }()

	log.Printf("igbot %s is ready as @%s", version, bot.Self.UserName)
	updates := tgbotapi.NewUpdate(0)
	updates.Timeout = 60
	messages := bot.GetUpdatesChan(updates)
	defer bot.StopReceivingUpdates()
	slots := make(chan struct{}, cfg.workers)
	var active sync.WaitGroup
	defer func() {
		cancel()
		active.Wait()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-healthErrors:
			return fmt.Errorf("health server stopped: %w", err)
		case update, ok := <-messages:
			if !ok {
				return errors.New("telegram update stream closed")
			}
			msg := update.Message
			if msg == nil || msg.Chat == nil {
				continue
			}
			text := msg.Text
			if text == "" {
				text = msg.Caption
			}
			if msg.Command() == "start" || msg.Command() == "help" {
				sendReply(bot, msg, "Send an Instagram reel or post link. I will send its video to this chat.")
				continue
			}
			igURL := ExtractInstagramURL(text)
			if igURL == "" {
				if msg.Chat.IsPrivate() {
					sendReply(bot, msg, "Please send a valid Instagram post or reel link.")
				}
				continue
			}
			select {
			case slots <- struct{}{}:
				active.Go(func() {
					defer func() { <-slots }()
					processInstagramMessage(ctx, bot, downloader, msg, igURL)
				})
			default:
				sendReply(bot, msg, "The bot is busy. Please try again shortly.")
			}
		}
	}
}

func healthHandler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ctx.Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func sendReply(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ReplyToMessageID = msg.MessageID
	_, _ = bot.Send(reply)
}

func processInstagramMessage(parent context.Context, bot *tgbotapi.BotAPI, downloader *Downloader, msg *tgbotapi.Message, igURL string) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	status, statusErr := bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Downloading Instagram video..."))
	defer func() {
		if statusErr == nil {
			_, _ = bot.Request(tgbotapi.NewDeleteMessage(msg.Chat.ID, status.MessageID))
		}
	}()
	_, _ = bot.Request(tgbotapi.NewChatAction(msg.Chat.ID, tgbotapi.ChatUploadVideo))

	videoPath, err := downloader.DownloadVideo(ctx, igURL)
	if err != nil {
		if parent.Err() != nil {
			return
		}
		log.Printf("Download failed: %v", err)
		text := "Cannot download this video. It may be private, unavailable, or too large. The operator may need to configure Instagram cookies."
		if errors.Is(err, ErrVideoTooLarge) {
			text = "The video exceeds the 50 MiB upload limit."
		}
		sendReply(bot, msg, text)
		return
	}
	defer func() {
		if err := os.RemoveAll(filepath.Dir(videoPath)); err != nil {
			log.Printf("Remove video files: %v", err)
		}
	}()
	video := tgbotapi.NewVideo(msg.Chat.ID, tgbotapi.FilePath(videoPath))
	video.ReplyToMessageID = msg.MessageID
	video.SupportsStreaming = true
	if _, err := bot.Send(video); err != nil {
		log.Print("Telegram video upload failed")
		sendReply(bot, msg, "Cannot send the video. Please try again later.")
		return
	}
	log.Print("Video sent")
}
