package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cesarpetrescu/ledger/internal/passkey"
	"github.com/cesarpetrescu/ledger/internal/store"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// maxPasskeyBytes bounds a sign-in assertion; an RS256 signature is the largest part.
const maxPasskeyBytes = 16 << 10

type passkeyView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Provider   string     `json:"provider"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func passkeyResponse(p store.OwnerPasskey) passkeyView {
	return passkeyView{ID: strconv.FormatInt(p.ID, 10), Name: p.Name, Provider: passkey.Provider(p.AAGUID), CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
}

func (s *Server) requirePasskeys(w http.ResponseWriter) bool {
	if s.passkeys == nil {
		writeError(w, http.StatusServiceUnavailable, "passkeys need LEDGER_PUBLIC_URL to use a domain name")
		return false
	}
	return true
}

func (s *Server) owner(ctx context.Context) (passkey.Owner, error) {
	handle, err := s.db.OwnerHandle(ctx)
	if err != nil {
		return passkey.Owner{}, err
	}
	passkeys, err := s.db.ListOwnerPasskeys(ctx)
	return passkey.Owner{Handle: handle, Passkeys: passkeys}, err
}

// beginPasskeyLogin issues a sign-in challenge: discoverable for a one-step passkey sign-in, or
// limited to the owner's passkeys for the step after the password, whose challenge identifier is
// the pending token.
func (s *Server) beginPasskeyLogin(w http.ResponseWriter, r *http.Request, purpose string, remember bool) {
	if !s.requirePasskeys(w) {
		return
	}
	ctx := r.Context()
	var assertion *protocol.CredentialAssertion
	var data *webauthn.SessionData
	var err error
	if purpose == store.ChallengeLogin {
		assertion, data, err = s.passkeys.BeginDiscoverableLogin()
	} else {
		var owner passkey.Owner
		if owner, err = s.owner(ctx); err == nil {
			assertion, data, err = s.passkeys.BeginLogin(owner)
		}
	}
	var options, sessionData json.RawMessage
	if err == nil {
		options, err = passkey.RequestOptions(assertion)
	}
	if err == nil {
		sessionData, err = json.Marshal(data)
	}
	var id string
	if err == nil {
		id, err = s.db.CreateWebAuthnChallenge(ctx, purpose, sessionData, "", remember)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if purpose == store.ChallengeLogin {
		writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "options": options})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkey_required": true, "pending": id, "options": options})
}

func (s *Server) passkeyLoginOptions(w http.ResponseWriter, r *http.Request) {
	if !s.signInAllowed(w, r) || s.lockedOut(w, r) {
		return
	}
	var input struct{}
	if err := decodeJSON(w, r, &input, maxLoginBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	exists, err := s.db.OwnerHasPasskeys(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !exists {
		writeError(w, http.StatusConflict, "no passkeys")
		return
	}
	s.beginPasskeyLogin(w, r, store.ChallengeLogin, false)
}

// passkeyLogin finishes a passkey sign-in: one step (challenge_id) or after the password (pending).
// Every refusal counts toward the same per-address lockout as a wrong password.
func (s *Server) passkeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.signInAllowed(w, r) || s.lockedOut(w, r) || !s.requirePasskeys(w) {
		return
	}
	var input struct {
		ChallengeID string          `json:"challenge_id"`
		Pending     string          `json:"pending"`
		Credential  json.RawMessage `json:"credential"`
		Remember    *bool           `json:"remember"`
	}
	if err := decodeJSON(w, r, &input, maxPasskeyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if (input.ChallengeID == "") == (input.Pending == "") {
		writeError(w, http.StatusBadRequest, "send challenge_id or pending")
		return
	}
	purpose, id, method := store.ChallengeLogin, input.ChallengeID, store.MethodPasskey
	if input.Pending != "" {
		purpose, id, method = store.ChallengePasswordLogin, input.Pending, store.MethodPasswordPasskey
	}
	ctx := r.Context()
	data, remember, err := s.db.ConsumeWebAuthnChallenge(ctx, id, purpose, "")
	if store.IsNotFound(err) {
		s.refusePasskey(w, r, errors.New("unknown, used, or expired challenge"))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if input.Remember != nil {
		remember = *input.Remember
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		s.internalError(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(input.Credential)
	if err != nil {
		s.refusePasskey(w, r, err)
		return
	}
	owner, err := s.owner(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The library checks the challenge, RP ID hash, origin, UP and UV flags, signature, user handle,
	// and that the credential is one of the owner's passkeys.
	var credential *webauthn.Credential
	if purpose == store.ChallengeLogin {
		_, credential, err = s.passkeys.ValidatePasskeyLogin(func([]byte, []byte) (webauthn.User, error) { return owner, nil }, session, parsed)
	} else {
		credential, err = s.passkeys.ValidateLogin(owner, session, parsed)
	}
	if err != nil {
		s.refusePasskey(w, r, err)
		return
	}
	authData := parsed.Response.AuthenticatorData
	err = s.db.UseOwnerPasskey(ctx, credential.ID, authData.Counter, authData.Flags.HasBackupState())
	if errors.Is(err, store.ErrPasskeyCounter) {
		s.refusePasskey(w, r, err)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.startSession(w, r, store.NewAdminSession{Remember: remember, Method: method})
}

func (s *Server) refusePasskey(w http.ResponseWriter, r *http.Request, err error) {
	logPasskeyError("sign-in", err)
	s.refuse(w, r, http.StatusUnauthorized, "passkey not accepted")
}

// logPasskeyError records why the library refused a ceremony; it never logs the credential itself.
func logPasskeyError(ceremony string, err error) {
	var detail *protocol.Error
	if errors.As(err, &detail) && detail.DevInfo != "" {
		log.Printf("admin: passkey %s refused: %v (%s)", ceremony, err, detail.DevInfo)
		return
	}
	log.Printf("admin: passkey %s refused: %v", ceremony, err)
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	passkeys, err := s.db.ListOwnerPasskeys(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	views := make([]passkeyView, len(passkeys))
	for i, p := range passkeys {
		views[i] = passkeyResponse(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkeys": views, "required": len(passkeys) > 0})
}

// passkeyRegistrationOptions starts registering a passkey. It needs the owner password as well as
// the session, so a stolen session cookie cannot add an attacker's passkey; the challenge is bound
// to this session.
func (s *Server) passkeyRegistrationOptions(w http.ResponseWriter, r *http.Request) {
	if !s.requirePasskeys(w) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, maxLoginBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !s.verifyOwnerPassword(w, r, input.Password) {
		return
	}
	ctx := r.Context()
	owner, err := s.owner(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	creation, data, err := s.passkeys.BeginRegistration(owner, webauthn.WithExclusions(owner.Exclusions()), webauthn.WithCredentialParameters(passkey.Algorithms()))
	var sessionData []byte
	if err == nil {
		sessionData, err = json.Marshal(data)
	}
	var id string
	if err == nil {
		id, err = s.db.CreateWebAuthnChallenge(ctx, store.ChallengeRegister, sessionData, sessionFrom(r).ID, false)
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "options": creation.Response})
}

func (s *Server) registerPasskey(w http.ResponseWriter, r *http.Request) {
	if !s.requirePasskeys(w) {
		return
	}
	var input struct {
		ChallengeID string          `json:"challenge_id"`
		Credential  json.RawMessage `json:"credential"`
		Name        string          `json:"name"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	name, ok := passkeyName(input.Name)
	if input.Name != "" && !ok {
		writeError(w, http.StatusBadRequest, "name must be 1 to 60 characters on one line")
		return
	}
	ctx := r.Context()
	data, _, err := s.db.ConsumeWebAuthnChallenge(ctx, input.ChallengeID, store.ChallengeRegister, sessionFrom(r).ID)
	if store.IsNotFound(err) {
		writeError(w, http.StatusBadRequest, "passkey registration expired; start again")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		s.internalError(w, r, err)
		return
	}
	owner, err := s.owner(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(input.Credential)
	var credential *webauthn.Credential
	if err == nil {
		credential, err = s.passkeys.CreateCredential(owner, session, parsed)
	}
	if err != nil {
		logPasskeyError("registration", err)
		writeError(w, http.StatusBadRequest, "passkey not accepted")
		return
	}
	if name == "" {
		name = passkey.DefaultName(credential.Authenticator.AAGUID)
	}
	saved, revoked, err := s.db.AddOwnerPasskey(ctx, store.OwnerPasskey{
		CredentialID: credential.ID, PublicKey: credential.PublicKey, SignCount: credential.Authenticator.SignCount,
		AAGUID: credential.Authenticator.AAGUID, Name: name, Transports: passkey.Transports(credential.Transport),
		AttestationFormat: credential.AttestationFormat, BackupEligible: credential.Flags.BackupEligible, BackupState: credential.Flags.BackupState,
	}, sessionFrom(r).ID)
	if errors.Is(err, store.ErrPasskeyExists) {
		writeError(w, http.StatusConflict, "this passkey is already registered")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// revoked counts the other sessions signed out because they had only proved the password.
	writeJSON(w, http.StatusCreated, map[string]any{"passkey": passkeyResponse(saved), "revoked": revoked})
}

// passkeyName trims a passkey name and reports whether it is 1 to 60 characters on one line.
func passkeyName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > 60 || strings.ContainsFunc(name, unicode.IsControl) {
		return "", false
	}
	return name, true
}

func pathPasskeyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "passkey not found")
		return 0, false
	}
	return id, true
}

func (s *Server) renamePasskey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathPasskeyID(w, r)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input, maxBodyBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	name, ok := passkeyName(input.Name)
	if !ok {
		writeError(w, http.StatusBadRequest, "name must be 1 to 60 characters on one line")
		return
	}
	saved, err := s.db.RenameOwnerPasskey(r.Context(), id, name)
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "passkey not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passkey": passkeyResponse(saved)})
}

// deletePasskey needs the owner password. Removing the last passkey returns sign-in to the
// password alone.
func (s *Server) deletePasskey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathPasskeyID(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input, maxLoginBytes); err != nil {
		writeDecodeError(w, err)
		return
	}
	if !s.verifyOwnerPassword(w, r, input.Password) {
		return
	}
	err := s.db.DeleteOwnerPasskey(r.Context(), id)
	if store.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "passkey not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
