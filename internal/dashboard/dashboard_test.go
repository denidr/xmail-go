package dashboard

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler_ServesIndexAtRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "<title>") {
		t.Errorf("body does not look like the HTML shell: %q", rec.Body.String())
	}
}

// TestHandler_SetsSecurityHeaders pins the hardening the dashboard
// relies on: CSP default-src 'self' (the API key lives in sessionStorage)
// and nosniff. Revalidation is forced so an upgraded binary can't pair a
// new index.html with a stale cached app.js.
func TestHandler_SetsSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))

	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
		t.Errorf("Content-Security-Policy = %q, want %q", got, "default-src 'self'")
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

func TestEmbeddedAssetsPresent(t *testing.T) {
	for _, name := range []string{"index.html", "app.js", "style.css"} {
		if _, err := fs.Stat(embedded, "assets/"+name); err != nil {
			t.Errorf("asset %s is missing from embed.FS: %v", name, err)
		}
	}
}

// TestIndexReferencesExistingAssets keeps index.html and the embedded
// files in sync: a renamed script would otherwise only fail in the
// browser, which no Go test covers.
func TestIndexReferencesExistingAssets(t *testing.T) {
	html := readIndex(t)
	for _, ref := range []string{"app.js", "style.css"} {
		if !strings.Contains(html, ref) {
			t.Errorf("index.html does not reference %s", ref)
		}
		if _, err := fs.Stat(embedded, "assets/"+ref); err != nil {
			t.Errorf("index.html references %s but it is not embedded: %v", ref, err)
		}
	}
}

// TestIndexHasNoInlineScriptOrStyle guards the page against the CSP the
// handler sets: inline <script>/<style>/style=""/on*= would be silently
// blocked by the browser, leaving a blank dashboard.
func TestIndexHasNoInlineScriptOrStyle(t *testing.T) {
	lower := strings.ToLower(readIndex(t))

	for i := 0; ; {
		open := strings.Index(lower[i:], "<script")
		if open < 0 {
			break
		}
		open += i
		end := strings.Index(lower[open:], ">")
		if end < 0 {
			t.Fatal("unterminated <script tag in index.html")
		}
		if tag := lower[open : open+end]; !strings.Contains(tag, "src=") {
			t.Errorf("index.html has an inline <script> (%q) — blocked by CSP default-src 'self'", tag)
		}
		i = open + end
	}

	if strings.Contains(lower, "<style") {
		t.Error("index.html has an inline <style> — blocked by CSP default-src 'self'")
	}
	if strings.Contains(lower, ` style="`) || strings.Contains(lower, " style='") {
		t.Error(`index.html has a style="" attribute — blocked by CSP default-src 'self'`)
	}
	for _, handler := range []string{"onclick=", "onchange=", "onsubmit=", "onload=", "oninput=", "onkeyup="} {
		if strings.Contains(lower, handler) {
			t.Errorf("index.html has an inline event handler (%s) — blocked by CSP default-src 'self'", handler)
		}
	}
}

func readIndex(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(embedded, "assets/index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	return string(b)
}
