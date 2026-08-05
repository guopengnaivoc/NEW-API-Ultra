package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	AccessTokenTTL        = 15 * time.Minute
	SecurityProofTTL      = 5 * time.Minute
	LoginSessionTTL       = 30 * 24 * time.Hour
	RefreshReplayWindow   = 30 * time.Second
	accessTokenUse        = "access"
	securityProofTokenUse = "security_proof"
	authTokenIssuer       = "new-api"
	authTokenAudience     = "new-api-dashboard"
)

var (
	ErrAuthTokenInvalid = errors.New("authentication token is invalid")
	ErrAuthTokenExpired = errors.New("authentication token has expired")
	ErrProofScope       = errors.New("security proof scope mismatch")
	ErrProofMethod      = errors.New("security proof method mismatch")
	ErrProofTarget      = errors.New("security proof target mismatch")
	ErrProofConsumed    = errors.New("security proof has already been consumed")
)

// CanonicalSecurityProofIDTarget validates a raw security-proof ID target
// without normalization, returns the input unchanged on success, and collapses
// every invalid form to ErrProofTarget. The explicit whitespace comparison is
// deliberate defense in depth and documents the wire contract even though
// strconv.Atoi also rejects whitespace.
func CanonicalSecurityProofIDTarget(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return "", ErrProofTarget
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 || strconv.Itoa(id) != raw {
		return "", ErrProofTarget
	}
	return raw, nil
}

// AuthIdentity is the server-validated identity attached to dashboard requests.
// Role, status and group are deliberately loaded from the user cache instead of JWT claims.
type AuthIdentity struct {
	UserID          int
	SessionID       string
	UserAuthVersion int64
	SessionVersion  int64
}

type authClaims struct {
	TokenUse        string `json:"token_use"`
	SessionID       string `json:"sid"`
	UserAuthVersion int64  `json:"uv"`
	SessionVersion  int64  `json:"sv"`
	jwt.RegisteredClaims
}

type oneTimeSecurityProofPayload struct {
	Method          string `json:"method"`
	Scope           string `json:"scope"`
	Target          string `json:"target"`
	UserAuthVersion int64  `json:"user_auth_version"`
	SessionVersion  int64  `json:"session_version"`
}

func authSigningKey(purpose string) []byte {
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, _ = mac.Write([]byte("new-api/auth/" + purpose + "/v1"))
	return mac.Sum(nil)
}

func IssueAccessToken(identity AuthIdentity) (string, int64, error) {
	return issueAccessTokenAt(identity, time.Now())
}

func issueAccessTokenAt(identity AuthIdentity, now time.Time) (string, int64, error) {
	if identity.UserID <= 0 || identity.SessionID == "" || identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 {
		return "", 0, ErrAuthTokenInvalid
	}
	expiresAt := now.Add(AccessTokenTTL)
	claims := authClaims{
		TokenUse:        accessTokenUse,
		SessionID:       identity.SessionID,
		UserAuthVersion: identity.UserAuthVersion,
		SessionVersion:  identity.SessionVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authTokenIssuer,
			Subject:   strconv.Itoa(identity.UserID),
			Audience:  jwt.ClaimStrings{authTokenAudience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-5 * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.NewString(),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(authSigningKey(accessTokenUse))
	return signed, expiresAt.Unix(), err
}

func ParseAccessToken(raw string) (AuthIdentity, error) {
	claims, err := parseAuthClaims(raw, accessTokenUse, authSigningKey(accessTokenUse))
	if err != nil {
		return AuthIdentity{}, err
	}
	userID, err := strconv.Atoi(claims.Subject)
	if err != nil || userID <= 0 || claims.SessionID == "" || claims.UserAuthVersion <= 0 || claims.SessionVersion <= 0 {
		return AuthIdentity{}, ErrAuthTokenInvalid
	}
	return AuthIdentity{
		UserID:          userID,
		SessionID:       claims.SessionID,
		UserAuthVersion: claims.UserAuthVersion,
		SessionVersion:  claims.SessionVersion,
	}, nil
}

// ParseDashboardAccessToken distinguishes new-api dashboard JWTs from opaque
// credentials. A token carrying the dashboard issuer, audience and a known
// token use is always treated as internal, even when its signature, lifetime
// or requested purpose is invalid, so it can never fall through to PAT or
// relay-token authentication.
func ParseDashboardAccessToken(raw string) (identity AuthIdentity, internal bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AuthIdentity{}, false, nil
	}
	claims := &authClaims{}
	parsed, _, parseErr := jwt.NewParser().ParseUnverified(raw, claims)
	if parseErr != nil || parsed == nil {
		return AuthIdentity{}, false, nil
	}
	audienceMatches := false
	for _, audience := range claims.Audience {
		if audience == authTokenAudience {
			audienceMatches = true
			break
		}
	}
	knownTokenUse := claims.TokenUse == accessTokenUse || claims.TokenUse == securityProofTokenUse
	if claims.Issuer != authTokenIssuer || !audienceMatches || !knownTokenUse {
		return AuthIdentity{}, false, nil
	}
	identity, err = ParseAccessToken(raw)
	return identity, true, err
}

// IssueOneTimeSecurityProof creates an opaque, database-backed proof for an
// security-sensitive operation. The proof is bound to the exact browser
// session, operation scope and target and can be consumed only once.
func IssueOneTimeSecurityProof(identity AuthIdentity, method, scope, target string) (string, int64, error) {
	method = strings.TrimSpace(method)
	scope = strings.TrimSpace(scope)
	if identity.UserID <= 0 || identity.SessionID == "" ||
		identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 ||
		method == "" || scope == "" || target == "" {
		return "", 0, ErrAuthTokenInvalid
	}
	payload, err := common.Marshal(oneTimeSecurityProofPayload{
		Method:          method,
		Scope:           scope,
		Target:          target,
		UserAuthVersion: identity.UserAuthVersion,
		SessionVersion:  identity.SessionVersion,
	})
	if err != nil {
		return "", 0, err
	}
	expiresAt := time.Now().Add(SecurityProofTTL)
	token, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   model.AuthFlowPurposeSecurityProof,
		UserId:    identity.UserID,
		SessionId: identity.SessionID,
		Payload:   string(payload),
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return "", 0, err
	}
	return token, expiresAt.Unix(), nil
}

// ConsumeOneTimeSecurityProof atomically validates and consumes a targeted
// security-sensitive operation proof. Validation errors roll back consumption
// so a proof cannot be burned by presenting it to the wrong operation.
func ConsumeOneTimeSecurityProof(
	raw string,
	identity AuthIdentity,
	requiredScope,
	requiredTarget string,
	allowedMethods []string,
) (string, error) {
	raw = strings.TrimSpace(raw)
	requiredScope = strings.TrimSpace(requiredScope)
	if raw == "" || identity.UserID <= 0 || identity.SessionID == "" ||
		identity.UserAuthVersion <= 0 || identity.SessionVersion <= 0 ||
		requiredScope == "" || requiredTarget == "" {
		return "", ErrAuthTokenInvalid
	}
	method := ""
	_, err := model.ConsumeAuthFlowWithAction(raw, model.AuthFlowMatch{
		Purpose:   model.AuthFlowPurposeSecurityProof,
		UserId:    identity.UserID,
		SessionId: identity.SessionID,
	}, func(_ *gorm.DB, flow *model.AuthFlow) error {
		var payload oneTimeSecurityProofPayload
		if err := common.UnmarshalJsonStr(flow.Payload, &payload); err != nil {
			return ErrAuthTokenInvalid
		}
		if payload.UserAuthVersion != identity.UserAuthVersion ||
			payload.SessionVersion != identity.SessionVersion {
			return ErrAuthTokenInvalid
		}
		if !hmac.Equal([]byte(payload.Scope), []byte(requiredScope)) {
			return ErrProofScope
		}
		if !hmac.Equal([]byte(payload.Target), []byte(requiredTarget)) {
			return ErrProofTarget
		}
		methodAllowed := len(allowedMethods) == 0
		for _, allowed := range allowedMethods {
			if hmac.Equal([]byte(payload.Method), []byte(allowed)) {
				methodAllowed = true
				break
			}
		}
		if !methodAllowed {
			return ErrProofMethod
		}
		method = payload.Method
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAuthFlowExpired):
			return "", ErrAuthTokenExpired
		case errors.Is(err, model.ErrAuthFlowConsumed):
			return "", ErrProofConsumed
		case errors.Is(err, model.ErrAuthFlowInvalid):
			return "", ErrAuthTokenInvalid
		default:
			return "", err
		}
	}
	return method, nil
}

func parseAuthClaims(raw, expectedUse string, key []byte) (*authClaims, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrAuthTokenInvalid
	}
	claims := &authClaims{}
	parsed, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrAuthTokenInvalid)
		}
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(authTokenIssuer), jwt.WithAudience(authTokenAudience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(5*time.Second))
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrAuthTokenExpired
		}
		return nil, fmt.Errorf("%w: %v", ErrAuthTokenInvalid, err)
	}
	if !parsed.Valid || claims.TokenUse != expectedUse || claims.ID == "" || claims.IssuedAt == nil || claims.NotBefore == nil {
		return nil, ErrAuthTokenInvalid
	}
	return claims, nil
}
