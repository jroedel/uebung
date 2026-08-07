package authapp

import (
	"net"
	"net/http"
	"strings"
)

// clientIP returns the address to rate-limit a request against.
//
// Behind Apache the socket peer is always 127.0.0.1, so limiting on RemoteAddr
// would put every visitor on the planet into one bucket — the whole internet
// sharing one allowance. X-Forwarded-For fixes that, but only when something
// trustworthy sets it: the header is caller-supplied, so trusting it on a
// directly-exposed server lets anyone mint a fresh identity per request and walk
// straight past the limiter.
//
// Hence trustProxy, which main sets from a flag rather than sniffing. When it is
// false the socket peer is the only thing believed.
//
// The right-most entry is taken, not the left-most: the proxy appends the peer it
// actually saw, so anything to the left was supplied by the client and is a
// forgery in this deployment.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr is not always host:port (httptest, unix sockets). Falling
		// back to the raw value keeps the limiter keyed on something stable
		// rather than collapsing every caller onto "".
		return r.RemoteAddr
	}

	return host
}

// isSecureRequest reports whether the browser's connection was HTTPS, which
// decides whether the session cookie may carry the Secure attribute.
//
// Terminating TLS at Apache means r.TLS is nil here even on an https:// request,
// so the proxy's X-Forwarded-Proto is the only evidence — and again only when the
// proxy is trusted. Getting this wrong in the safe direction (omitting Secure)
// would ship session cookies over plain HTTP, so main defaults trustProxy on for
// the deployed configuration and local development opts out.
func isSecureRequest(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}

	if trustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}

	return false
}
