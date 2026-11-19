package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultRepository = "ssukru/local-worker"
	DefaultAPIBase    = "https://api.github.com"
)

type Asset struct {
	Name        string
	DownloadURL string
	APIURL      string
}

type Release struct {
	Tag       string
	Version   string
	Archive   Asset
	Checksums Asset
}

type CommandRunner func(context.Context, int64, string, ...string) ([]byte, error)

type Client struct {
	HTTPClient *http.Client
	APIBase    string
	Repository string
	Token      string
	Run        CommandRunner
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	URL                string `json:"url"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

func NewClient() *Client {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	return &Client{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		APIBase:    DefaultAPIBase,
		Repository: DefaultRepository,
		Token:      token,
		Run:        runCommand,
	}
}

func AssetName(tag, goos, goarch string) (string, error) {
	if goos != "darwin" && goos != "linux" {
		return "", fmt.Errorf("unsupported OS for updates: %s", goos)
	}
	if goarch != "amd64" && goarch != "arm64" {
		return "", fmt.Errorf("unsupported architecture for updates: %s", goarch)
	}
	normalized, err := NormalizeVersion(tag)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("lw-%s-%s-%s.tar.gz", normalized, goos, goarch), nil
}

func (c *Client) Resolve(ctx context.Context, requested, goos, goarch string) (Release, error) {
	endpoint := fmt.Sprintf("repos/%s/releases/latest", c.repository())
	if requested != "" {
		tag, err := NormalizeVersion(requested)
		if err != nil {
			return Release{}, err
		}
		endpoint = fmt.Sprintf("repos/%s/releases/tags/%s", c.repository(), tag)
	}

	var payload githubRelease
	if err := c.getJSON(ctx, endpoint, &payload); err != nil {
		if ctx.Err() != nil {
			return Release{}, ctx.Err()
		}
		if fallbackErr := c.ghJSON(ctx, endpoint, &payload); fallbackErr != nil {
			return Release{}, fmt.Errorf("resolve GitHub release: %w", err)
		}
	}

	tag, err := NormalizeVersion(payload.TagName)
	if err != nil {
		return Release{}, fmt.Errorf("release returned an invalid tag: %w", err)
	}
	archiveName, err := AssetName(tag, goos, goarch)
	if err != nil {
		return Release{}, err
	}

	var archive, checksums Asset
	for _, item := range payload.Assets {
		asset := Asset{Name: item.Name, DownloadURL: item.BrowserDownloadURL, APIURL: item.URL}
		switch item.Name {
		case archiveName:
			archive = asset
		case "checksums.txt":
			checksums = asset
		}
	}
	if archive.Name == "" {
		return Release{}, fmt.Errorf("release %s does not contain %s", tag, archiveName)
	}
	if checksums.Name == "" {
		return Release{}, fmt.Errorf("release %s does not contain checksums.txt", tag)
	}
	return Release{Tag: tag, Version: strings.TrimPrefix(tag, "v"), Archive: archive, Checksums: checksums}, nil
}

func (c *Client) Download(ctx context.Context, asset Asset, maxBytes int64) ([]byte, error) {
	body, err := c.get(ctx, asset.DownloadURL, maxBytes)
	if err == nil {
		return body, nil
	}
	if c.Run == nil || asset.APIURL == "" {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	out, fallbackErr := c.Run(ctx, maxBytes, "gh", "api", "-H", "Accept: application/octet-stream", asset.APIURL)
	if fallbackErr != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("download %s exceeds %d bytes", asset.Name, maxBytes)
	}
	return out, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, target any) error {
	body, err := c.get(ctx, strings.TrimRight(c.apiBase(), "/")+"/"+endpoint, 4<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (c *Client) ghJSON(ctx context.Context, endpoint string, target any) error {
	if c.Run == nil {
		return fmt.Errorf("gh fallback unavailable")
	}
	body, err := c.Run(ctx, 4<<20, "gh", "api", endpoint)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}

func (c *Client) get(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("download URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "local-worker-updater")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	reader := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func runCommand(ctx context.Context, maxBytes int64, name string, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(io.LimitReader(stdout, maxBytes+1))
	if int64(len(body)) > maxBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("%s output exceeds %d bytes", name, maxBytes)
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	return body, nil
}

func (c *Client) apiBase() string {
	if c.APIBase == "" {
		return DefaultAPIBase
	}
	return c.APIBase
}

func (c *Client) repository() string {
	if c.Repository == "" {
		return DefaultRepository
	}
	return c.Repository
}
