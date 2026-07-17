package nats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
	"github.com/johnnycube/cairn-core/internal/usecase/enrollment"
)

// AuthCalloutSubscriber subscribes to NATS's auth-callout subject and
// routes each request through the enrollment.ProcessAuthCallout use
// case. The reply is a signed AuthorizationResponseClaims JWT that
// either admits the connection (with a user JWT scoped to permissions
// derived from the enrollment) or rejects it with a reason string.
//
// Wire diagram from architecture.md §4.5:
//
//	worker --CONNECT(token,nkey)--> nats-server
//	                                      |
//	                                      v
//	                              $SYS.REQ.USER.AUTH (subject)
//	                                      |
//	                                      v
//	                              this subscriber
//	                                      |
//	                                      v
//	                              ProcessAuthCallout (use case)
//	                                      |
//	                                      v
//	                              CredentialIssuer (signs user-JWT)
//	                                      |
//	                                      v
//	                              reply: signed AuthorizationResponse
//	                                      |
//	                                      v
//	                              nats-server admits or rejects
//
// The signing key for the AuthorizationResponse is the same NATS account
// key the CredentialIssuer uses for user JWTs — NATS server is
// configured with this account as the `auth_callout.account`, so it
// trusts responses signed by this key.
type AuthCalloutSubscriber struct {
	bus       *Bus
	processor *enrollment.ProcessAuthCallout
	issuer    *CredentialIssuer
	logger    *slog.Logger

	// xkeyKP, if set, is used to decrypt encrypted auth-callout requests
	// (NATS feature: auth-callout responses can be encrypted with an
	// X25519 key so credentials don't traverse the wire in plain JWT
	// form). For v1 we operate without xkey encryption — the auth
	// account is configured trusted and the wire is TLS-protected. The
	// nkey is kept here as a hook for future hardening.
	xkeyKP nkeys.KeyPair
}

// NewAuthCalloutSubscriber wires the subscriber. Caller is responsible
// for calling Start to actually subscribe.
func NewAuthCalloutSubscriber(
	bus *Bus,
	processor *enrollment.ProcessAuthCallout,
	issuer *CredentialIssuer,
	logger *slog.Logger,
) *AuthCalloutSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthCalloutSubscriber{
		bus:       bus,
		processor: processor,
		issuer:    issuer,
		logger:    logger.With("component", "auth_callout"),
	}
}

// AuthCalloutSubject is NATS's well-known subject for auth-callout
// requests. Subscribers on this subject in the SYS account (or any
// account marked trusted in the auth_callout config block) handle
// every connection attempt.
const AuthCalloutSubject = "$SYS.REQ.USER.AUTH"

// Start subscribes to the auth-callout subject and dispatches each
// incoming auth request. Returns a Subscription handle to control
// shutdown.
func (s *AuthCalloutSubscriber) Start(ctx context.Context) (port.Subscription, error) {
	sub, err := s.bus.Conn().Subscribe(AuthCalloutSubject, func(msg *nats.Msg) {
		s.handleAuthRequest(ctx, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("subscribe to %s: %w", AuthCalloutSubject, err)
	}
	s.logger.Info("auth-callout subscriber active", "subject", AuthCalloutSubject)
	return &coreSubscription{sub: sub}, nil
}

// handleAuthRequest is invoked once per NATS connection attempt.
// Steps:
//
//  1. Decode the incoming jwt.AuthorizationRequest claims.
//  2. Pull the worker's CONNECT options (token, name, nkey, etc.).
//  3. Call ProcessAuthCallout.
//  4. On success, build an AuthorizationResponseClaims with the user JWT.
//  5. On rejection, build a response with Error set and Jwt empty.
//  6. Sign + publish the reply.
func (s *AuthCalloutSubscriber) handleAuthRequest(ctx context.Context, msg *nats.Msg) {
	req, err := jwt.DecodeAuthorizationRequestClaims(string(msg.Data))
	if err != nil {
		s.logger.Warn("decode auth request failed", "error", err)
		s.replyError(ctx, msg, "", "invalid_request: decode failed")
		return
	}

	connectOpts := req.ConnectOptions

	in := enrollment.AuthCalloutInput{
		EnrollmentToken:  connectOpts.Token,
		UserNKeyPublic:   req.UserNkey,
		WorkerName:       connectOpts.Name,
		WorkerInstanceID: connectOpts.Lang, // workers stuff instance id in `lang` slot (no dedicated field)
		WorkerVersion:    connectOpts.Version,
		ClientHost:       req.ClientInformation.Host,
	}

	res, err := s.processor.Execute(ctx, in)
	if err != nil {
		var rejection *enrollment.AuthRejection
		if errors.As(err, &rejection) {
			s.logger.Info("auth rejected",
				"reason", rejection.Reason,
				"detail", rejection.Detail,
				"worker_name", in.WorkerName,
				"client_host", in.ClientHost,
			)
			s.replyError(ctx, msg, req.UserNkey, rejection.Reason)
			return
		}
		s.logger.Error("auth-callout internal error", "error", err)
		s.replyError(ctx, msg, req.UserNkey, "internal_error")
		return
	}

	s.logger.Info("auth admitted",
		"enrollment_id", res.EnrollmentID,
		"grant_id", res.GrantID,
		"worker_name", in.WorkerName,
		"expires_at", res.ExpiresAt,
	)
	s.replyAdmit(ctx, msg, req.UserNkey, res.UserJWT, res.AccountPublicKey)
}

// replyAdmit signs and publishes an AuthorizationResponseClaims with the
// user JWT embedded. NATS server validates the response signature
// against the auth account it's configured to trust.
func (s *AuthCalloutSubscriber) replyAdmit(
	ctx context.Context,
	msg *nats.Msg,
	userNKey, userJWT, accountPub string,
) {
	resp := jwt.NewAuthorizationResponseClaims(userNKey)
	resp.Audience = accountPub
	resp.Jwt = userJWT

	s.publishResponse(ctx, msg, resp)
}

// replyError signs and publishes an AuthorizationResponseClaims with an
// error string. Reason strings are stable so operators can grep logs +
// metrics by reason class.
func (s *AuthCalloutSubscriber) replyError(
	ctx context.Context,
	msg *nats.Msg,
	userNKey, reason string,
) {
	sub := userNKey
	if sub == "" {
		// Auth-callout responses must have a `sub`; fall back to a sentinel
		// that NATS treats as an anonymous failed connect.
		sub = "UAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" // 56 chars
	}
	resp := jwt.NewAuthorizationResponseClaims(sub)
	resp.Error = reason
	s.publishResponse(ctx, msg, resp)
}

// publishResponse signs and sends the reply.
func (s *AuthCalloutSubscriber) publishResponse(
	ctx context.Context,
	original *nats.Msg,
	resp *jwt.AuthorizationResponseClaims,
) {
	accountPub, err := s.issuer.AccountPublicKey(ctx)
	if err != nil {
		s.logger.Error("auth-callout: load account public key", "error", err)
		return
	}
	resp.IssuerAccount = accountPub

	// Reuse the issuer's account keypair to sign the auth response. The
	// CredentialIssuer caches this; reaching for the cache directly here
	// avoids a re-decrypt.
	kp, _, err := s.issuer.loadActiveAccountKey(ctx)
	if err != nil {
		s.logger.Error("auth-callout: load signing key", "error", err)
		return
	}

	encoded, err := resp.Encode(kp)
	if err != nil {
		s.logger.Error("auth-callout: encode response", "error", err)
		return
	}

	if original.Reply == "" {
		s.logger.Warn("auth-callout: request had no reply subject")
		return
	}
	if err := s.bus.Conn().Publish(original.Reply, []byte(encoded)); err != nil {
		s.logger.Error("auth-callout: publish reply", "error", err)
	}
}

// _ is a sanity-check that domain.SigningKeyPurposeNATSAccount is used —
// keep this package coupled to the constant so a rename in domain
// surfaces here at compile time.
var _ = domain.SigningKeyPurposeNATSAccount
