package authapp

import (
	"github.com/jroedel/uebung/business/domain/identity/identitybus"
	"github.com/jroedel/uebung/business/types/email"
	"github.com/jroedel/uebung/business/types/nickname"
	"github.com/jroedel/uebung/foundation/errs"
)

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

// setNicknameRequest chooses or changes a display name.
type setNicknameRequest struct {
	Nickname string `json:"nickname"`
}

// userResponse describes the signed-in account.
//
// It carries no identifiers beyond what the UI shows. In particular there is no
// session value and no token: a response body ends up in caches, logs and
// screenshots, and none of those should be able to sign anyone in.
type userResponse struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`

	// Nickname is empty until the learner has settled on one. The client uses
	// that emptiness to decide whether to prompt.
	Nickname string `json:"nickname"`

	// Suggestion is a free name to offer as the skip option, and is sent only
	// when Nickname is empty — there is nothing to suggest to someone who
	// already has a name, and generating one anyway would be a database lookup
	// on every /auth/me call for no purpose.
	//
	// It is not reserved. Between this response and the learner accepting it,
	// somebody else may take it, in which case the server quietly assigns a
	// different one; see identitybus.AssignNickname.
	Suggestion string `json:"suggestion,omitzero"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// fromBusUserResponse flattens the strong types explicitly.
//
// suggestion is passed in rather than read from the user because it is not part
// of the account: it is a proposal the caller decided to make.
func fromBusUserResponse(u identitybus.User, suggestion nickname.Nickname) userResponse {
	return userResponse{
		Email:      u.Email.String(),
		Verified:   u.Verified,
		Nickname:   u.Nickname.String(),
		Suggestion: suggestion.String(),
	}
}

// toBusEmail parses the request primitive into the strong type. All validation
// for this boundary happens here and nowhere else.
func toBusEmail(raw string) (email.Email, error) {
	return email.Parse(raw)
}

// toBusNickname parses the request primitive into the strong type, accumulating
// the failure as a field error so the client can attach the message to the
// input it belongs to.
func toBusNickname(req setNicknameRequest) (nickname.Nickname, error) {
	var fieldErrors errs.FieldErrors

	name, err := nickname.Parse(req.Nickname)
	fieldErrors.Add("nickname", err)

	if !fieldErrors.Empty() {
		return nickname.Nickname{}, fieldErrors.ErrorOrNil()
	}

	return name, nil
}
