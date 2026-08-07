package authapp

import "github.com/jroedel/uebung/business/domain/identity/identitybus"

// Wire types for the auth surface. Primitives only, as at any API edge: no
// business/types strong type appears here, and nothing that could carry a
// credential is ever put in a response.

// requestLoginRequest asks for a sign-in link.
type requestLoginRequest struct {
	Email string `json:"email"`
}

// requestLoginResponse is deliberately contentless. It says the same thing for a
// known address, an unknown address and a rate-limited one, so the endpoint
// cannot be used to discover who has an account.
type requestLoginResponse struct {
	Message string `json:"message"`
}

// userResponse describes the signed-in account.
//
// It carries no identifiers beyond what the UI shows. In particular there is no
// session value and no token: a response body ends up in caches, logs and
// screenshots, and none of those should be able to sign anyone in.
type userResponse struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// fromBusUserResponse flattens the strong types explicitly.
func fromBusUserResponse(u identitybus.User) userResponse {
	return userResponse{
		Email:    u.Email.String(),
		Verified: u.Verified,
	}
}
