package update

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"darwin", "arm64", "lw-v0.3.0-darwin-arm64.tar.gz"},
		{"darwin", "amd64", "lw-v0.3.0-darwin-amd64.tar.gz"},
		{"linux", "arm64", "lw-v0.3.0-linux-arm64.tar.gz"},
		{"linux", "amd64", "lw-v0.3.0-linux-amd64.tar.gz"},
	} {
		got, err := AssetName("0.3.0", tc.goos, tc.goarch)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("AssetName() = %q, want %q", got, tc.want)
		}
	}
	if _, err := AssetName("0.3.0", "windows", "amd64"); err == nil {
		t.Fatal("AssetName() expected unsupported OS error")
	}
	if _, err := AssetName("0.3.0", "linux", "386"); err == nil {
		t.Fatal("AssetName() expected unsupported architecture error")
	}
}

func TestClientResolveAndDownload(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ssukru/local-worker/releases/latest", "/repos/ssukru/local-worker/releases/tags/v0.3.0":
			fmt.Fprintf(w, `{"tag_name":"v0.3.0","assets":[{"name":"lw-v0.3.0-darwin-arm64.tar.gz","browser_download_url":%q,"url":%q},{"name":"checksums.txt","browser_download_url":%q,"url":%q}]}`,
				server.URL+"/archive", server.URL+"/api/archive", server.URL+"/checksums", server.URL+"/api/checksums")
		case "/archive":
			_, _ = w.Write([]byte("archive"))
		case "/checksums":
			_, _ = w.Write([]byte("checksums"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, Repository: DefaultRepository}
	for _, requested := range []string{"", "0.3.0"} {
		release, err := client.Resolve(context.Background(), requested, "darwin", "arm64")
		if err != nil {
			t.Fatal(err)
		}
		if release.Version != "0.3.0" || release.Archive.Name != "lw-v0.3.0-darwin-arm64.tar.gz" {
			t.Fatalf("unexpected release: %+v", release)
		}
		body, err := client.Download(context.Background(), release.Archive, 1024)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != "archive" {
			t.Fatalf("Download() = %q", body)
		}
	}
}

func TestClientSendsTokenAndEnforcesLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		_, _ = w.Write([]byte(strings.Repeat("x", 10)))
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), Token: "secret"}
	if _, err := client.Download(context.Background(), Asset{Name: "asset", DownloadURL: server.URL}, 5); err == nil {
		t.Fatal("Download() expected size error")
	}
}

func TestClientResolveRejectsMissingAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.3.0","assets":[]}`))
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), APIBase: server.URL, Run: nil}
	if _, err := client.Resolve(context.Background(), "", "linux", "amd64"); err == nil {
		t.Fatal("Resolve() expected missing asset error")
	}
}
