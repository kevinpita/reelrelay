package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("REELRELAY_TEST_HELPER"); mode != "" {
		runFakeDownloader(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeDownloader(mode string) {
	args := os.Args[1:]
	var output string
	for i, arg := range args {
		if arg == "--output" {
			output = strings.ReplaceAll(args[i+1], "%(ext)s", "mp4")
		}
		if arg == "--cookies" {
			info, err := os.Stat(args[i+1])
			if err != nil || info.Mode().Perm() != 0o600 {
				os.Exit(2)
			}
			if err := os.WriteFile(args[i+1], []byte("updated cookies"), 0o600); err != nil {
				os.Exit(3)
			}
		}
	}
	encoded, _ := json.Marshal(args)
	if err := os.WriteFile(os.Getenv("REELRELAY_TEST_ARGS"), encoded, 0o600); err != nil {
		os.Exit(4)
	}
	switch mode {
	case "fail":
		fmt.Fprint(os.Stderr, "private-cookie-value")
		os.Exit(1)
	case "timeout":
		time.Sleep(time.Minute)
	case "empty":
		return
	case "escape":
		fmt.Println("/etc/passwd")
		return
	case "symlink":
		if err := os.Symlink(os.Getenv("REELRELAY_TEST_ARGS"), output); err != nil {
			os.Exit(5)
		}
	default:
		if err := os.WriteFile(output, []byte("fake video"), 0o600); err != nil {
			os.Exit(6)
		}
		if mode == "oversized" {
			if err := os.Truncate(output, maxVideoBytes+1); err != nil {
				os.Exit(7)
			}
		}
	}
	fmt.Println(output)
}

func fakeDownloader(t *testing.T, mode string) (*Downloader, string, string) {
	t.Helper()
	root := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("TMPDIR", root)
	t.Setenv("REELRELAY_TEST_HELPER", mode)
	t.Setenv("REELRELAY_TEST_ARGS", argsFile)
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &Downloader{ytDlpPath: path}, root, argsFile
}

func TestExtractInstagramURL(t *testing.T) {
	for _, tt := range []struct{ input, expected string }{
		{"https://www.instagram.com/reel/C8qX-0gOIaF/", "https://www.instagram.com/reel/C8qX-0gOIaF/"},
		{"Watch https://instagram.com/p/abc/?igsh=xyz wow", "https://instagram.com/p/abc/?igsh=xyz"},
		{"(https://instagram.com/p/abc/).", "https://instagram.com/p/abc/"},
		{"https://instagram.com/share/abc/", "https://instagram.com/share/abc/"},
		{"https://instagram.com/share/reel/abc/", "https://instagram.com/share/reel/abc/"},
		{"https://instagram.com/profile/ then https://instagram.com/reel/abc/", "https://instagram.com/reel/abc/"},
		{"No link", ""},
		{"https://instagram.com/accounts/login/", ""},
		{"https://instagram.com/redirect?url=https://example.com", ""},
		{"https://instagram.com.evil.example/reel/abc/", ""},
		{"https://instagram.com@evil.example/reel/abc/", ""},
		{"https://instagram.com:123/reel/abc/", ""},
	} {
		if got := ExtractInstagramURL(tt.input); got != tt.expected {
			t.Errorf("ExtractInstagramURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEnsureYtDlp(t *testing.T) {
	t.Setenv("YT_DLP_PATH", "")
	t.Setenv("PATH", t.TempDir())
	if _, err := ensureYtDlp(); err == nil {
		t.Fatal("missing dependency must fail without a download")
	}
	t.Setenv("YT_DLP_PATH", filepath.Join(t.TempDir(), "missing"))
	if _, err := ensureYtDlp(); err == nil {
		t.Fatal("invalid explicit path must fail")
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YT_DLP_PATH", path)
	if got, err := ensureYtDlp(); err != nil || got != path {
		t.Fatalf("got %q, %v; want explicit executable", got, err)
	}
}

func TestDownloadVideo(t *testing.T) {
	d, _, argsFile := fakeDownloader(t, "success")
	cookies := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(cookies, []byte("original cookies"), 0o400); err != nil {
		t.Fatal(err)
	}
	d.cookiesFile = cookies
	path, err := d.DownloadVideo(t.Context(), "https://instagram.com/reel/abc/")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "fake video" {
		t.Fatalf("download = %q, %v", data, err)
	}
	if data, err := os.ReadFile(cookies); err != nil || string(data) != "original cookies" {
		t.Fatalf("source cookies changed: %q, %v", data, err)
	}
	var args []string
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args[len(args)-2:], []string{"--", "https://instagram.com/reel/abc/"}) {
		t.Fatalf("URL is not separated from options: %v", args)
	}
	if args[0] != "--ignore-config" {
		t.Fatalf("user config must be ignored: %v", args)
	}
}

func TestDownloadFailuresCleanUp(t *testing.T) {
	for _, mode := range []string{"fail", "empty", "escape", "symlink", "oversized", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			d, root, _ := fakeDownloader(t, mode)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			_, err := d.DownloadVideo(ctx, "https://instagram.com/p/abc/")
			if err == nil || strings.Contains(err.Error(), "private-cookie-value") {
				t.Fatalf("expected a safe error, got %v", err)
			}
			if mode == "oversized" && !errors.Is(err, ErrVideoTooLarge) {
				t.Fatalf("want ErrVideoTooLarge, got %v", err)
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("want deadline error, got %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary files remain: %v, %v", entries, err)
			}
		})
	}
}

func TestPrepareSessionCookies(t *testing.T) {
	d := &Downloader{sessionID: "secret-session"}
	path, err := d.prepareCookies(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), ".instagram.com\tTRUE\t/\tTRUE\t0\tsessionid\tsecret-session") {
		t.Fatalf("invalid domain-scoped cookie: %q, %v", data, err)
	}
}

func TestDownloadRejectsUnsupportedURL(t *testing.T) {
	d := &Downloader{ytDlpPath: "/must-not-run"}
	if _, err := d.DownloadVideo(t.Context(), "https://example.com/reel/abc/"); err == nil {
		t.Fatal("unsupported URL was accepted")
	}
}
