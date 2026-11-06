package authflow

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const UsernameClaim = "http://lfx.dev/claims/username"

const idTokenUsernameClaim = "https://sso.linuxfoundation.org/claims/username" // #nosec G101

type Claims struct {
	Username string
	Subject  string
	Issuer   string
	Expiry   time.Time
}

func DecodeClaims(token string) (Claims, error) {
	payload, err := jwtPayload(token)
	if err != nil {
		return Claims{}, err
	}
	var raw struct {
		Username string `json:"http://lfx.dev/claims/username"`
		Subject  string `json:"sub"`
		Issuer   string `json:"iss"`
		Exp      int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Claims{}, fmt.Errorf("decode token claims: %w", err)
	}
	claims := Claims{Username: raw.Username, Subject: raw.Subject, Issuer: raw.Issuer}
	if raw.Exp != 0 {
		claims.Expiry = time.Unix(raw.Exp, 0)
	}
	return claims, nil
}

func IdentityFromIDToken(idToken string) string {
	payload, err := jwtPayload(idToken)
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	username, _ := claims[idTokenUsernameClaim].(string)
	email, _ := claims["email"].(string)
	sub, _ := claims["sub"].(string)
	switch {
	case username != "" && email != "":
		return fmt.Sprintf("%s (%s)", username, email)
	case username != "":
		return username
	case email != "":
		return email
	default:
		return sub
	}
}

func jwtPayload(token string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode token payload: %w", err)
	}
	return payload, nil
}
