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
// forgery in this deployment. That is correct for exactly one proxy hop, which is
// the deployment this app documents; a second hop would need the count to be
// configurable.
//
// Note what this is NOT used for. Cookie security is decided once at startup
// (see Config.SecureCookies), never from a request header. It used to be
// inferred here from X-Forwarded-Proto, which meant a deployment that forgot
// -trust-proxy — or a proxy that does not send that header, as Apache's
// mod_proxy_http does not — silently issued session cookies without Secure and
// without the __Host- prefix, with no symptom because the site still worked.
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
