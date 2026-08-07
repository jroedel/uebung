// Package authapp is the App layer for signing in: the HTTP surface over the
// identity domain.
//
// Three endpoints do the work. POST /auth/request emails a link. GET
// /auth/callback exchanges the link for a session cookie. POST /auth/logout
// destroys it. GET /auth/me tells the client who, if anyone, is signed in, so the
// browser can decide between the sign-in screen and the deck.
//
// The recurring theme is that this package refuses to leak. It never says whether
// an address has an account, never distinguishes an expired token from a forged
// one, and never writes a token into a log or a redirect that could outlive it.
package authapp

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/userid"
)

// SessionCookieName is the cookie holding the session secret. The __Host- prefix
// is a browser-enforced guarantee: a cookie so named is only accepted when it is
// Secure, has no Domain, and has Path=/, which stops a sibling subdomain or a
// plain-HTTP page from writing a session cookie for us.
//
// The prefix only works over HTTPS, so the insecure name is used when the app is
// served over plain HTTP in local development.
const (
	SessionCookieName         = "__Host-uebung_session"
	insecureSessionCookieName = "uebung_session"
)

// Config wires the auth surface.
type Config struct {
	Identity *identitybus.Business
	Log      *slog.Logger
	Now      func() time.Time

	// TrustProxy tells the handlers to believe X-Forwarded-For and
	// X-Forwarded-Proto. True only when something we control terminates TLS in
	// front; see clientip.go for why this is a flag and not a guess.
	TrustProxy bool

	// PerIPPerHour and PerEmailPerHour bound how many sign-in mails one client
	// address, and one target address, can cause per hour.
	PerIPPerHour    int
	PerEmailPerHour int
}

// App serves the auth endpoints.
type App struct {
	identity   *identitybus.Business
	log        *slog.Logger
	now        func() time.Time
	trustProxy bool
	byIP       *limiter
	byEmail    *limiter
}

// Defaults chosen so a real person who mistypes their address a few times is
// never blocked, while a script is.
const (
	defaultPerIPPerHour    = 10
	defaultPerEmailPerHour = 5
)

// New constructs the auth app.
func New(cfg Config) *App {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	perIP := cfg.PerIPPerHour
	if perIP <= 0 {
		perIP = defaultPerIPPerHour
	}

	perEmail := cfg.PerEmailPerHour
	if perEmail <= 0 {
		perEmail = defaultPerEmailPerHour
	}

	return &App{
		identity:   cfg.Identity,
		log:        cfg.Log,
		now:        now,
		trustProxy: cfg.TrustProxy,
		byIP:       newLimiter(perIP, time.Hour, now),
		byEmail:    newLimiter(perEmail, time.Hour, now),
	}
}

// Handler returns the auth routes.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /auth/request", a.handleRequest)
	mux.HandleFunc("GET /auth/callback", a.handleCallback)
	mux.HandleFunc("POST /auth/logout", a.handleLogout)
	mux.HandleFunc("GET /auth/me", a.handleMe)

	return mux
}

// handleRequest emails a sign-in link.
//
// It answers 202 for every well-formed address, whether or not an account exists
// and even when the rate limiter suppressed the mail. Anything else would let a
// stranger ask this endpoint who has an account here.
func (a *App) handleRequest(w http.ResponseWriter, r *http.Request) {
	var req requestLoginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "expected a JSON body with an email field"})

		return
	}

	addr, err := toBusEmail(req.Email)
	if err != nil {
		// The one thing worth telling the caller apart: a malformed address is
		// the user's own typo, and silently swallowing it would leave them
		// waiting for mail that was never going to come.
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "that does not look like an email address"})

		return
	}

	ip := clientIP(r, a.trustProxy)
	a.byIP.sweep()

	// Both budgets are consulted, and both are charged, so a caller cannot spend
	// someone else's allowance to shield their own.
	ipOK := a.byIP.allow(ip)
	emailOK := a.byEmail.allow(addr.String())

	switch {
	case !ipOK || !emailOK:
		// Deliberately still 202. A 429 here would confirm to a script that it
		// had found a real, popular address.
		a.log.Warn("sign-in link suppressed by rate limit",
			"ip", ip, "email_domain", addr.Domain(), "ip_ok", ipOK, "email_ok", emailOK)
	default:
		if err := a.identity.RequestLogin(r.Context(), addr); err != nil {
			// A failure to send is ours, not theirs, and the caller cannot act on
			// the detail. It is logged with the domain only — never the address —
			// so operational logs do not accumulate a list of who uses the site.
			a.log.Error("sending sign-in link", "email_domain", addr.Domain(), "err", err)
			writeJSON(w, http.StatusInternalServerError,
				errorResponse{Error: "could not send the sign-in email just now; please try again"})

			return
		}
	}

	writeJSON(w, http.StatusAccepted, requestLoginResponse{
		Message: "If that address can receive mail, a sign-in link is on its way.",
	})
}

// handleCallback exchanges a magic-link token for a session cookie.
//
// It responds with a redirect rather than JSON because the browser arrives here
// by following a link from an email, and it redirects to "/" specifically so the
// token stops being part of the visible URL: left in the address bar it would be
// bookmarked, put in a Referer, and pasted into support threads.
func (a *App) handleCallback(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")

	raw, user, err := a.identity.Redeem(r.Context(), token)
	if err != nil {
		if !errors.Is(err, identitybus.ErrInvalidCredential) {
			a.log.Error("redeeming sign-in link", "err", err)
		}

		// One destination for every failure: unknown, expired, already used. The
		// client shows "that link didn't work, ask for another".
		http.Redirect(w, r, "/?signin=expired", http.StatusSeeOther)

		return
	}

	a.setSessionCookie(w, r, raw)
	a.log.Info("signed in", "user", user.ID.String(), "email_domain", user.Email.Domain())

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout destroys the session server-side and clears the cookie.
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if raw := a.sessionSecret(r); raw != "" {
		if err := a.identity.Logout(r.Context(), raw); err != nil {
			a.log.Error("signing out", "err", err)
		}
	}

	a.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

// handleMe reports the signed-in account, or 401 when there is none. The client
// calls this on load to choose between the sign-in screen and the deck.
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := a.identity.Authenticate(r.Context(), a.sessionSecret(r))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "not signed in"})

		return
	}

	writeJSON(w, http.StatusOK, fromBusUserResponse(user))
}

// sessionSecret reads the session cookie under whichever name this deployment
// uses.
func (a *App) sessionSecret(r *http.Request) string {
	name := a.cookieName(r)
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}

	return ""
}

func (a *App) cookieName(r *http.Request) string {
	if isSecureRequest(r, a.trustProxy) {
		return SessionCookieName
	}

	return insecureSessionCookieName
}

func (a *App) setSessionCookie(w http.ResponseWriter, r *http.Request, raw string) {
	secure := isSecureRequest(r, a.trustProxy)

	http.SetCookie(w, &http.Cookie{
		Name:  a.cookieName(r),
		Value: raw,
		Path:  "/",
		// HttpOnly keeps the session out of reach of any script on the page, so a
		// content injection cannot read it.
		HttpOnly: true,
		Secure:   secure,
		// Lax rather than Strict: the sign-in link arrives as a top-level
		// navigation from an email client, and Strict would drop the cookie on
		// exactly that first request.
		SameSite: http.SameSiteLaxMode,
		Expires:  a.now().Add(a.identity.SessionTTL()),
		MaxAge:   int(a.identity.SessionTTL().Seconds()),
	})
}

func (a *App) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName(r),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r, a.trustProxy),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// --- small HTTP helpers -----------------------------------------------------

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 8<<10))
	dec.DisallowUnknownFields()

	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// Auth responses say who is signed in; a cache anywhere in the path holding
	// one and replaying it to the next visitor would be a session mix-up.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// toBusEmail parses the request primitive into the strong type. All validation
// for this boundary happens here and nowhere else.
func toBusEmail(raw string) (email.Email, error) {
	return email.Parse(raw)
}

// UserForRequest resolves a request's session cookie to a learner id.
//
// This is what the study app calls on every request. It is exported here, rather
// than the study app reading cookies itself, so that the knowledge of how a
// session is carried lives in exactly one package — and so studyapp never has to
// import this one, which the layering forbids. main passes an *App in as the
// study app's Authenticator.
func (a *App) UserForRequest(r *http.Request) (userid.UserID, bool) {
	user, err := a.identity.Authenticate(r.Context(), a.sessionSecret(r))
	if err != nil {
		return userid.UserID{}, false
	}

	return user.ID, true
}
