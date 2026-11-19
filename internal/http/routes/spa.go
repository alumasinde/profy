package routes

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/alumasinde/profy/internal/http/middleware"
)

// AppPrefixes are client-side-routed application areas served by the Svelte shell (§6.3).
var AppPrefixes = []string{"/auth", "/account", "/provider", "/admin"}

var inlineScript = regexp.MustCompile(`(?s)<script(?:\s[^>]*)?>(.*?)</script>`)

// spa serves the compiled SvelteKit app: hashed assets under /_app/ with long-lived caching,
// and the SPA fallback page for application paths with noindex. Inline bootstrap scripts are
// allowed by hash only, keeping the CSP free of 'unsafe-inline'.
type spa struct {
	assets     fs.FS
	shell      []byte
	cspExtra   string // script hashes
	styleExtra string // style attribute hashes
}

func newSPA(appFS fs.FS) *spa {
	s := &spa{assets: appFS}
	if b, err := fs.ReadFile(appFS, "200.html"); err == nil {
		s.shell = b
		var hashes []string
		for _, m := range inlineScript.FindAllSubmatch(b, -1) {
			if len(strings.TrimSpace(string(m[1]))) == 0 {
				continue
			}
			sum := sha256.Sum256(m[1])
			hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
		}
		s.cspExtra = strings.Join(hashes, " ")
	}
	s.styleExtra = styleAttrHashes(appFS)
	return s
}

var styleAttr = regexp.MustCompile(`style="([^"]+)"`)

// styleAttrHashes finds static style attributes emitted by the framework (for example the
// SvelteKit route announcer) and allows exactly those via 'unsafe-hashes', instead of
// allowing arbitrary inline styles.
func styleAttrHashes(appFS fs.FS) string {
	seen := map[string]bool{}
	var out []string
	_ = fs.WalkDir(appFS, "_app", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return nil
		}
		b, err := fs.ReadFile(appFS, p)
		if err != nil {
			return nil
		}
		for _, m := range styleAttr.FindAllSubmatch(b, -1) {
			sum := sha256.Sum256(m[1])
			h := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
		return nil
	})
	if len(out) == 0 {
		return ""
	}
	return "'unsafe-hashes' " + strings.Join(out, " ")
}

// shellHandler serves the application shell.
func (s *spa) shellHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-cache")
	if s.shell == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("The web application has not been built. Run `npm ci && npm run build` in frontend/ and rebuild the server.\n"))
		return
	}
	w.Header().Set("Content-Security-Policy", middleware.CSP(s.cspExtra, s.styleExtra))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.shell)
}

// assetHandler serves /_app/* files. Immutable (hashed) files get a one-year cache.
func (s *spa) assetHandler(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	b, err := fs.ReadFile(s.assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if strings.HasPrefix(name, "_app/immutable/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	_, _ = w.Write(b)
}
