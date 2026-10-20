package remoteapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"1loop/internal/core"
)

func TestItemImage(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nnot really")
	images := func(index int) ([]core.ImagePart, bool) {
		if index != 3 {
			return nil, false
		}
		return []core.ImagePart{{Mime: "image/png", Data: base64.StdEncoding.EncodeToString(png)}, {Mime: "text/html", Data: "PGI+"}}, true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/chats/{id}/items/{index}/images/{n}", func(w http.ResponseWriter, r *http.Request) { ItemImage(w, r, images) })
	get := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		return recorder
	}
	if r := get("/api/chats/c/items/3/images/0"); r.Code != 200 || r.Header().Get("Content-Type") != "image/png" || r.Body.String() != string(png) {
		t.Fatalf("image: %d %q %q", r.Code, r.Header().Get("Content-Type"), r.Body.String())
	}
	for _, path := range []string{"/api/chats/c/items/3/images/2", "/api/chats/c/items/9/images/0", "/api/chats/c/items/x/images/0", "/api/chats/c/items/3/images/1"} {
		if r := get(path); r.Code != 404 {
			t.Fatalf("%s: %d", path, r.Code)
		}
	}
}
