package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func sampleBackground(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.NRGBA{R: 50, G: 120, B: 190, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestManagedAssetsAndAppearancePersist(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	source := filepath.Join(t.TempDir(), "自定义背景.png")
	original := sampleBackground(t)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	asset, err := a.ImportLocalAsset("background", source, "")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "自定义背景" || asset.MIMEType != "image/png" || asset.Size != int64(len(original)) {
		t.Fatalf("incorrect imported metadata: %+v", asset)
	}
	appearance := defaultAppearance()
	appearance.BackgroundID = asset.ID
	appearance.UIScale = 1.25
	appearance.TerminalFontSize = 17
	appearance.BackgroundOpacity = .24
	if _, err := a.store.SaveAppearance(appearance); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.RenameAsset(asset.ID, "海边"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	list := reopened.List()
	if !reflect.DeepEqual(list.Appearance, appearance) || len(list.Assets) != 1 || list.Assets[0].Name != "海边" {
		t.Fatalf("asset/settings not persisted: %+v", list)
	}
	content, err := reopened.AssetData(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if content.DataURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(original) {
		t.Fatal("managed asset data differs from source")
	}
	if err := reopened.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err = OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.List().Appearance.BackgroundID != "builtin:none" || reopened.List().Appearance.UIScale != 1.25 || len(reopened.List().Assets) != 0 {
		t.Fatal("deleting selected background did not persist a usable fallback")
	}
	if _, err := os.Stat(filepath.Join(a.store.dir, "assets", asset.ID)); !os.IsNotExist(err) {
		t.Fatal("managed copy was not removed")
	}
	got, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("import/delete changed the source file")
	}
}

func TestAssetValidationAndRollback(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind, filename string
		data           []byte
	}{
		{"background", "payload.svg", []byte("<svg></svg>")},
		{"background", "renamed.png", []byte("not an image")},
		{"font", "renamed.ttf", sampleBackground(t)},
		{"font", "empty.woff2", []byte("wOF2")},
		{"unknown", "hello.png", sampleBackground(t)},
	} {
		if _, err := s.ImportAsset(tc.kind, tc.filename, "test", tc.data); err == nil {
			t.Fatalf("accepted invalid import %s", tc.filename)
		}
	}
	for _, id := range []string{"../config.json", "../../outside", "/etc/passwd", strings.Repeat("z", 48)} {
		if _, err := s.AssetData(id); err == nil {
			t.Fatalf("accepted unsafe asset ID %s", id)
		}
		if err := s.DeleteAsset(id); err == nil {
			t.Fatalf("accepted unsafe delete ID %s", id)
		}
	}
	appearance := defaultAppearance()
	appearance.BackgroundID = "../config.json"
	if _, err := s.SaveAppearance(appearance); err == nil {
		t.Fatal("invalid resource selected")
	}
	appearance = defaultAppearance()
	appearance.UIScale = 0
	if _, err := s.SaveAppearance(appearance); err == nil {
		t.Fatal("zero UI scale accepted")
	}
	asset, err := s.ImportAsset("background", "test.png", "test", sampleBackground(t))
	if err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(s.dir, EncryptedConfigName)
	if err := os.Rename(configFile, configFile+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(configFile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAsset(asset.ID); err == nil {
		t.Fatal("failed config commit did not report error")
	}
	if len(s.List().Assets) != 1 {
		t.Fatal("failed deletion changed metadata")
	}
	if _, err := s.AssetData(asset.ID); err != nil {
		t.Fatal("failed deletion did not restore asset copy", err)
	}
}

func TestNamedProxyPersistenceAndConnect(t *testing.T) {
	observed := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Host + " " + r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	defer server.Close()
	host, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	proxy, err := a.store.SaveProxy(ManagedProxy{Name: "工作代理", Proxy: ProxyConfig{Type: "http", Host: host, Port: port, User: "alice", Password: "proxy-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Proxy.Password != "" || !proxy.Proxy.HasPassword {
		t.Fatal("proxy password leaked or password indicator lost")
	}
	p, err := a.store.Save(Profile{Name: "bound", Host: "only-proxy-resolves.invalid", User: "root", Secret: "ssh-secret", ProxyID: proxy.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	proxy.Name = "重命名"
	if _, err := a.store.SaveProxy(proxy); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(a.store.dir)
	if err != nil {
		t.Fatal(err)
	}
	a.store = reopened
	serialized, _ := json.Marshal(reopened.List())
	if bytes.Contains(serialized, []byte("proxy-secret")) || bytes.Contains(serialized, []byte("ssh-secret")) {
		t.Fatal("public config contains credentials")
	}
	if _, err := a.Connect(context.Background(), p.ID, ""); err == nil || !strings.Contains(err.Error(), "代理认证失败") {
		t.Fatalf("connection did not use the managed HTTP proxy: %v", err)
	}
	want := "only-proxy-resolves.invalid:22 Basic " + base64.StdEncoding.EncodeToString([]byte("alice:proxy-secret"))
	if got := <-observed; got != want {
		t.Fatalf("proxy target/auth mismatch: %q", got)
	}
	if err := reopened.DeleteProxy(proxy.ID); err == nil {
		t.Fatal("deleted bound proxy")
	}
	proxy.Proxy.ClearPassword = true
	if _, err := reopened.SaveProxy(proxy); err != nil {
		t.Fatal(err)
	}
	resolved, err := reopened.ResolveProxy(proxy.ID)
	if err != nil || resolved.Password != "" {
		t.Fatal("clearing password failed")
	}
	p.ProxyID = ""
	if _, err := reopened.Save(p, false); err != nil {
		t.Fatal(err)
	}
	if err := reopened.DeleteProxy(proxy.ID); err != nil {
		t.Fatal(err)
	}
	p.ProxyID = proxy.ID
	if _, err := reopened.Save(p, false); err == nil {
		t.Fatal("bound deleted proxy")
	}
}

func TestAssetHTTPUsesAuthenticatedJSONBridge(t *testing.T) {
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.baseURL = "http://wails.localhost"
	handler := a.Handler(fstest.MapFS{"index.html": {Data: []byte("test")}})
	// A real PNG larger than the generic API's 2 MB limit exercises the separate
	// import cap instead of accepting a tiny fixture through the wrong decoder.
	var pngData bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 1024, 768))); err != nil {
		t.Fatal(err)
	}
	if pngData.Len() <= 2<<20 {
		t.Fatal("fixture should exercise a large import")
	}
	body, _ := json.Marshal(map[string]string{"kind": "background", "filename": "test.png", "name": "From browser", "data": base64.StdEncoding.EncodeToString(pngData.Bytes())})
	request := httptest.NewRequest(http.MethodPost, "http://wails.localhost/api/assets", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal("asset import lacks API authentication")
	}
	request = httptest.NewRequest(http.MethodPost, "http://wails.localhost/api/assets", bytes.NewReader(body))
	request.Header.Set("X-CloudShell-Token", a.Token())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	var asset ManagedAsset
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "http://wails.localhost/api/assets/"+asset.ID+"/data", nil)
	request.Header.Set("X-CloudShell-Token", a.Token())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var content AssetContent
	if err := json.Unmarshal(response.Body.Bytes(), &content); err != nil || response.Code != http.StatusOK || !strings.HasPrefix(content.DataURL, "data:image/png;base64,") {
		t.Fatal("asset is unavailable through JSON bridge", response.Body.String())
	}
}

func TestCustomFontImportAndFallback(t *testing.T) {
	// The distributed font is also a realistic browser/native import fixture.
	font, err := os.ReadFile(filepath.Join("..", "..", "web", "assets", "fonts", "jetbrains-mono.woff2"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	asset, err := s.ImportAsset("font", "my-font.woff2", "我的字体", font)
	if err != nil {
		t.Fatal(err)
	}
	if asset.MIMEType != "font/woff2" {
		t.Fatal("font MIME type is wrong")
	}
	appearance := defaultAppearance()
	appearance.FontID = asset.ID
	if _, err := s.SaveAppearance(appearance); err != nil {
		t.Fatal(err)
	}
	appearance.BackgroundID = asset.ID
	if _, err := s.SaveAppearance(appearance); err == nil {
		t.Fatal("a font can be selected as an image")
	}
	if err := s.DeleteAsset(asset.ID); err != nil {
		t.Fatal(err)
	}
	if s.List().Appearance.FontID != defaultAppearance().FontID {
		t.Fatal("deleting selected custom font left a broken selection")
	}
}
