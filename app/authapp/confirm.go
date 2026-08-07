package authapp

import (
	"crypto/subtle"
	"html/template"
	"net/http"
	"time"
)

// The confirmation cookie names mirror the session ones: the __Host- prefix when
// the deployment is HTTPS, a plain name otherwise.
const (
	confirmCookieName         = "__Host-uebung_confirm"
	insecureConfirmCookieName = "uebung_confirm"
)

// confirmTTL bounds how long a rendered confirmation page stays submittable. It
// only has to outlive a person reading one sentence and tapping a button.
const confirmTTL = 10 * time.Minute

// confirmData fills the confirmation page.
type confirmData struct {
	Email string
	Token string
	Nonce string
}

// confirmPage asks the person to confirm before a link is spent.
//
// html/template escapes every interpolation in its context, so the address —
// which is attacker-chosen, since anyone can request a link for any address —
// cannot break out into markup. It is shown precisely so that a victim steered
// here by someone else sees an address they do not recognise before acting.
//
// Styling is inline and minimal: this page is served before the app shell and
// should not depend on it loading.
var confirmPage = template.Must(template.New("confirm").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="referrer" content="no-referrer" />
<title>Sign in — Übung Club</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
         background: #0f1226; color: #eef1ff; display: flex; min-height: 100dvh;
         align-items: center; justify-content: center; margin: 0; padding: 24px; }
  .card { background: #1e2340; border-radius: 20px; padding: 28px 26px; max-width: 360px; width: 100%;
          box-shadow: 0 18px 50px rgba(0,0,0,.45); text-align: center; }
  h1 { font-size: 22px; margin: 0 0 10px; }
  p { color: #9aa3c7; margin: 0 0 18px; line-height: 1.45; }
  .addr { color: #eef1ff; font-weight: 700; word-break: break-all; }
  button { border: 0; border-radius: 14px; background: #4f8cff; color: #0b0e1f; font-weight: 800;
           font-size: 16px; padding: 12px 22px; width: 100%; cursor: pointer; }
  .note { font-size: 12px; margin: 16px 0 0; }
</style>
</head>
<body>
  <main class="card">
    <h1>Sign in to Übung Club</h1>
    <p>Continue as <span class="addr">{{.Email}}</span>?</p>
    <form method="POST" action="/auth/callback">
      <input type="hidden" name="token" value="{{.Token}}" />
      <input type="hidden" name="nonce" value="{{.Nonce}}" />
      <button type="submit">Yes, sign me in</button>
    </form>
    <p class="note">If that isn’t your address, close this page — nothing has happened yet.</p>
  </main>
</body>
</html>
`))

func (a *App) confirmCookieName() string {
	if a.secure {
		return confirmCookieName
	}

	return insecureConfirmCookieName
}

// setConfirmCookie stores the nonce the form must echo back.
//
// SameSite=Strict is the whole mechanism. A cross-site POST — an auto-submitting
// form on a page the victim visits — does not carry a Strict cookie, so it cannot
// produce a matching nonce, and the redemption is refused. Lax would not do:
// Lax permits top-level cross-site POSTs in some browsers' interpretations, and
// this is exactly the request that must be same-site.
func (a *App) setConfirmCookie(w http.ResponseWriter, nonce string) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.confirmCookieName(),
		Value:    nonce,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(confirmTTL.Seconds()),
	})
}

func (a *App) clearConfirmCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.confirmCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// confirmNonceMatches reports whether this POST came from a confirmation page we
// rendered in this same browser.
//
// The nonce is compared, not merely required: a present-but-different value is a
// forgery, and an empty form field must never match a missing cookie.
func (a *App) confirmNonceMatches(r *http.Request) bool {
	c, err := r.Cookie(a.confirmCookieName())
	if err != nil || c.Value == "" {
		return false
	}

	submitted := r.PostForm.Get("nonce")
	if submitted == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(submitted)) == 1
}
