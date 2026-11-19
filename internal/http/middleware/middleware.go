// Package middleware holds cross-cutting HTTP middleware.
package middleware

import (
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/alumasinde/profy/internal/http/response"
	"github.com/alumasinde/profy/internal/security"
	"github.com/alumasinde/profy/internal/shared/reqctx"
)

// Chain applies middleware so the first listed runs outermost.
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// RequestContext assigns a request ID and records the client IP and user agent.
// X-Forwarded-For is trusted only when trustProxy is set (behind a known reverse proxy).
func RequestContext(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if !trustProxy || !requestIDPattern.MatchString(id) {
				id = security.RandomToken(12)
			}
			w.Header().Set("X-Request-ID", id)
			ua := r.UserAgent()
			if len(ua) > 255 {
				ua = ua[:255]
			}
			ctx := reqctx.WithRequestID(r.Context(), id)
			ctx = reqctx.WithClient(ctx, clientIP(r, trustProxy), ua)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The right-most address was appended by our proxy.
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Logging writes one structured line per request. Query strings are not logged.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			attrs := []any{
				"method", r.Method, "path", r.URL.Path, "status", rec.status,
				"bytes", rec.bytes, "duration_ms", time.Since(start).Milliseconds(),
				"request_id", reqctx.RequestID(r.Context()), "ip", reqctx.ClientIP(r.Context()),
			}
			if p := reqctx.PrincipalFrom(r.Context()); p != nil {
				attrs = append(attrs, "user", p.PublicID)
			}
			level := slog.LevelInfo
			if rec.status >= 500 {
				level = slog.LevelError
			}
			log.Log(r.Context(), level, "http_request", attrs...)
		})
	}
}

// Recover converts panics into 500 responses without leaking details.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()),
						"request_id", reqctx.RequestID(r.Context()))
					response.Fail(w, r, response.New(http.StatusInternalServerError, response.CodeInternal, "Something went wrong. Please try again."))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets baseline headers. Handlers may override Content-Security-Policy.
func SecurityHeaders(hsts bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(self), payment=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Content-Security-Policy", BaseCSP(""))
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BaseCSP returns the restrictive policy (no inline scripts or styles). extraScript adds hash sources.
func BaseCSP(extraScript string) string { return CSP(extraScript, "") }

// CSP builds the policy with optional extra script and style hash sources.
func CSP(extraScript, extraStyle string) string {
	script, style := "'self'", "'self'"
	if extraScript != "" {
		script += " " + extraScript
	}
	if extraStyle != "" {
		style += " " + extraStyle
	}
	return "default-src 'self'; script-src " + script + "; style-src " + style + "; img-src 'self' data: https:; " +
		"font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"
}

// CSRF cookie and header names.
const (
	CSRFHeader = "X-CSRF-Token"
)

// CSRFCookieName returns the CSRF cookie name; the __Host- prefix requires Secure cookies.
func CSRFCookieName(secure bool) string {
	if secure {
		return "__Host-hl_csrf"
	}
	return "hl_csrf"
}

// CSRF enforces double-submit tokens plus an Origin check on unsafe methods for
// cookie-authenticated requests (§6.4). Requests with an Authorization bearer header
// are exempt (CSRF does not apply to bearer tokens). It also issues the cookie when missing.
func CSRF(secure bool, siteOrigin func(*http.Request) string) func(http.Handler) http.Handler {
	name := CSRFCookieName(secure)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, _ := r.Cookie(name)
			if cookie == nil || len(cookie.Value) < 32 {
				SetCSRFCookie(w, secure, security.RandomToken(32))
			}
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				next.ServeHTTP(w, r)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(origin, siteOrigin(r), r) {
				response.Fail(w, r, response.ErrCSRF)
				return
			}
			header := r.Header.Get(CSRFHeader)
			if cookie == nil || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
				response.Fail(w, r, response.ErrCSRF)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetCSRFCookie writes a new CSRF token cookie (readable by the app's JavaScript).
func SetCSRFCookie(w http.ResponseWriter, secure bool, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName(secure), Value: token, Path: "/", Secure: secure,
		HttpOnly: false, SameSite: http.SameSiteLaxMode, MaxAge: 0,
	})
}

func sameOrigin(origin, site string, r *http.Request) bool {
	o, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if s, err := url.Parse(site); err == nil && strings.EqualFold(o.Scheme, s.Scheme) && strings.EqualFold(o.Host, s.Host) {
		return true
	}
	// Same host the request was addressed to (covers local development on another port/host).
	return strings.EqualFold(o.Host, r.Host)
}
