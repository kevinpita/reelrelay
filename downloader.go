package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxVideoBytes = 50 * 1024 * 1024

var (
	instagramURLRegex = regexp.MustCompile(`https?://(?:www\.)?instagram\.com/[^\s<>]+`)
	mediaURLRegex     = regexp.MustCompile(`https?://(?:www\.|mobile\.)?(?:instagram\.com|twitter\.com|x\.com)/[^\s<>]+`)
	twitterPathRegex  = regexp.MustCompile(`^/(?:[a-zA-Z0-9_]{1,15}/status|i/web/status)/[0-9]+(?:/(?:video|photo)/[0-9]+)?/?$`)
	mediaPathRegex    = regexp.MustCompile(`^/(?:p|reel|reels|tv|share(?:/(?:reel|p))?)/[a-zA-Z0-9_-]+/?$`)
	ErrVideoTooLarge  = errors.New("video exceeds Telegram's 50 MiB upload limit")
)

// ExtractInstagramURL returns the first supported Instagram media link.
func ExtractInstagramURL(text string) string {
	for _, match := range instagramURLRegex.FindAllString(text, -1) {
		match = strings.TrimRight(match, ".,!?:;)'\"]}")
		if validInstagramURL(match) {
			return match
		}
	}
	return ""
}

// ExtractMediaURL returns the first supported Instagram or Twitter/X post link.
func ExtractMediaURL(text string) string {
	for _, match := range mediaURLRegex.FindAllString(text, -1) {
		match = strings.TrimRight(match, ".,!?:;)'\"]}")
		if validMediaURL(match) {
			return match
		}
	}
	return ""
}

func validMediaURL(raw string) bool {
	return validInstagramURL(raw) || validTwitterURL(raw)
}

func validTwitterURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return false
	}
	switch u.Host {
	case "twitter.com", "www.twitter.com", "mobile.twitter.com", "x.com", "www.x.com", "mobile.x.com":
		return twitterPathRegex.MatchString(u.Path)
	default:
		return false
	}
}

func validInstagramURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") &&
		(u.Host == "instagram.com" || u.Host == "www.instagram.com") &&
		u.User == nil && mediaPathRegex.MatchString(u.Path)
}

// Downloader has immutable settings. Each download owns its files and cookies.
type Downloader struct {
	ytDlpPath      string
	cookiesFile    string
	cookiesBrowser string
	sessionID      string
}

func NewDownloader() (*Downloader, error) {
	path, err := ensureYtDlp()
	if err != nil {
		return nil, err
	}
	d := &Downloader{
		ytDlpPath:      path,
		cookiesFile:    os.Getenv("INSTAGRAM_COOKIES_FILE"),
		cookiesBrowser: os.Getenv("INSTAGRAM_COOKIES_BROWSER"),
		sessionID:      os.Getenv("INSTAGRAM_SESSION_ID"),
	}
	if strings.ContainsAny(d.sessionID, "\r\n\t") {
		return nil, errors.New("INSTAGRAM_SESSION_ID contains invalid characters")
	}
	if d.cookiesFile == "" {
		if _, err := os.Stat("cookies.txt"); err == nil {
			d.cookiesFile = "cookies.txt"
		}
	}
	if d.cookiesFile != "" {
		info, err := os.Stat(d.cookiesFile)
		if err != nil {
			return nil, fmt.Errorf("read cookies file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("cookies file must be a regular file")
		}
	}
	return d, nil
}

func ensureYtDlp() (string, error) {
	name := os.Getenv("YT_DLP_PATH")
	if name == "" {
		name = "yt-dlp"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("find yt-dlp: %w; install it with devenv or set YT_DLP_PATH", err)
	}
	return path, nil
}

// DownloadVideo returns a file in a private temporary directory.
// The caller must remove the directory after upload. Failed downloads clean up here.
func (d *Downloader) DownloadVideo(ctx context.Context, rawURL string) (path string, err error) {
	if !validMediaURL(rawURL) {
		return "", errors.New("unsupported media URL")
	}
	tempDir, err := os.MkdirTemp("", "reelrelay_*")
	if err != nil {
		return "", fmt.Errorf("create download directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tempDir)
		}
	}()

	args := []string{
		"--ignore-config", "--no-cache-dir", "--no-playlist", "--playlist-items", "1",
		"--format", "best[ext=mp4]/best", "--max-filesize", "50M",
		"--output", filepath.Join(tempDir, "video.%(ext)s"),
		"--print", "after_move:filepath", "--no-progress", "--no-warnings",
		"--socket-timeout", "30", "--retries", "2",
	}
	// Instagram authentication settings must not affect Twitter/X downloads.
	if validInstagramURL(rawURL) {
		cookies, err := d.prepareCookies(tempDir)
		if err != nil {
			return "", err
		}
		if cookies != "" {
			args = append(args, "--cookies", cookies)
		} else if d.cookiesBrowser != "" {
			args = append(args, "--cookies-from-browser", d.cookiesBrowser)
		}
	}
	args = append(args, "--", rawURL)
	cmd := exec.CommandContext(ctx, d.ytDlpPath, args...)
	// A child such as FFmpeg can keep output pipes open after yt-dlp exits.
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// yt-dlp output can contain credentials. Do not include it in logs or replies.
		return "", fmt.Errorf("yt-dlp failed: %w", err)
	}
	path = strings.TrimSpace(string(output))
	if filepath.Dir(path) != tempDir || !strings.HasPrefix(filepath.Base(path), "video.") {
		return "", errors.New("yt-dlp did not return a video file; the post may be unavailable or too large")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("check downloaded video: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", errors.New("download is not a non-empty regular file")
	}
	if info.Size() > maxVideoBytes {
		return "", ErrVideoTooLarge
	}
	return path, nil
}

func (d *Downloader) prepareCookies(tempDir string) (string, error) {
	var content []byte
	if d.cookiesFile != "" {
		var err error
		content, err = os.ReadFile(d.cookiesFile)
		if err != nil {
			return "", fmt.Errorf("read cookies file: %w", err)
		}
	} else if d.sessionID != "" {
		content = []byte("# Netscape HTTP Cookie File\n.instagram.com\tTRUE\t/\tTRUE\t0\tsessionid\t" + d.sessionID + "\n")
	} else {
		return "", nil
	}
	// yt-dlp writes its cookie jar on exit. Kubernetes Secret mounts are read-only.
	path := filepath.Join(tempDir, "cookies.txt")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", fmt.Errorf("prepare private cookie jar: %w", err)
	}
	return path, nil
}
