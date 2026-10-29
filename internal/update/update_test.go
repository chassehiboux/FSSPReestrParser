package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVersionAndAssetOrigin(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.2.0", "0.1.9", true}, {"v0.1.0", "0.1.0", false}, {"v0.1.0", "0.1.0-dev", true}, {"v0.1.0", "0.2.0", false}, {"invalid", "0.1.0", false}} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Fatalf("%s %s: %v", c.a, c.b, got)
		}
	}
	_, e := assetURL(Release{Assets: []Asset{{Name: AssetName, URL: "https://evil.test/app.exe"}}}, AssetName)
	if e == nil {
		t.Fatal("untrusted asset origin accepted")
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCheckSeesNewReleaseDespitePreviousCache(t *testing.T) {
	oldClient, oldVersion := client, Version
	defer func() { client = oldClient; Version = oldVersion }()
	Version = "0.1.1"
	calls := 0
	client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		version := "0.1.1"
		if calls > 1 {
			version = "0.1.2"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"tag_name":"v%s","assets":[]}`, version)))}, nil
	})}
	dir := t.TempDir()
	r, e := Check(context.Background(), dir)
	if e != nil || r != nil {
		t.Fatalf("First check %v %v", r, e)
	}
	r, e = Check(context.Background(), dir)
	if e != nil || r == nil || r.Version != "v0.1.2" || calls != 2 {
		t.Fatalf("New release hidden by cache: %v %v %d", r, e, calls)
	}
}
func TestDownloadVerifiesChecksumAndWindowsImage(t *testing.T) {
	dir := t.TempDir()
	// Антивирус Windows может кратковременно удерживать только что записанный EXE.
	t.Cleanup(func() {
		for attempt := 0; attempt < 50; attempt++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	old := client
	defer func() { client = old }()
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.ReadFile(self)
	if e != nil {
		t.Fatal(e)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(binary))
	checksum := strings.Repeat("0", 64)
	base := "https://github.com/" + Repository + "/releases/download/v9.9.9/"
	release := Release{Version: "v9.9.9", Assets: []Asset{{Name: AssetName, URL: base + AssetName}, {Name: "checksums.txt", URL: base + "checksums.txt"}}}
	client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		data := binary
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			data = []byte(checksum + "  " + AssetName + "\n")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(data)), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	if _, err := Download(context.Background(), release, dir, func(int64, int64) {}); err == nil {
		t.Fatal("Wrong checksum accepted")
	}
	checksum = sum
	path, err := Download(context.Background(), release, dir, func(int64, int64) {})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Size() != int64(len(binary)) {
		t.Fatal("Package missing or truncated", err)
	}
}
