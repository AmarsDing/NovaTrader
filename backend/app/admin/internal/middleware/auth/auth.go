package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	middleware "server/app/admin/internal/middleware"

	"github.com/go-kratos/kratos/v2/middleware/auth/jwt"
	"github.com/go-kratos/kratos/v2/transport"
	"github.com/go-kratos/kratos/v2/transport/http"
	jwtV5 "github.com/golang-jwt/jwt/v5"
)

const (
	ClaimAuthorityId = "authorityId"
)

type MyCustomClaims struct {
	Foo string `json:"foo"`
	jwtV5.RegisteredClaims
}
type SecurityUser struct {
	Path        string
	Method      string
	Domain      string
	AuthorityId string
}

func NewSecurityUser() middleware.SecurityUser {
	return &SecurityUser{}
}

func (su *SecurityUser) ParseFromContext(ctx context.Context) error {
	claims, ok := jwt.FromContext(ctx)
	if !ok {
		return errors.New("jwt claim missing")
	}
	if mc, ok := claims.(jwtV5.MapClaims); ok {
		if jti, ok := mc["jti"].(string); ok {
			su.AuthorityId = jti
		}
	}
	if su.AuthorityId == "" {
		return errors.New("jwt claim missing jti")
	}

	if tr, ok := transport.FromServerContext(ctx); ok {

		if ht, ok := tr.(*http.Transport); ok {
			su.Path = ht.PathTemplate()
		} else {
			su.Path = tr.Operation()
		}
		su.Method = "ALL"
		su.Domain = tr.Endpoint()
	} else {
		return errors.New("jwt claim missing")
	}

	return nil
}

func (su *SecurityUser) GetSubject() string {
	return su.AuthorityId
}

func (su *SecurityUser) GetObject() string {
	return su.Path
}

func (su *SecurityUser) GetAction() string {
	return su.Method
}
func (su *SecurityUser) GetDomain() string {
	return su.Domain
}
func (su *SecurityUser) CreateAccessJwtToken(secretKey []byte) string {
	claims := MyCustomClaims{
		Foo: "bar",
		RegisteredClaims: jwtV5.RegisteredClaims{
			ExpiresAt: jwtV5.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwtV5.NewNumericDate(time.Now()),
			NotBefore: jwtV5.NewNumericDate(time.Now()),
			ID:        su.AuthorityId,
		},
	}
	token := jwtV5.NewWithClaims(jwtV5.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(secretKey)
	if err != nil {
		return ""
	}
	return signedToken
}

func (su *SecurityUser) ParseAccessJwtTokenFromContext(ctx context.Context) error {
	claims, ok := jwt.FromContext(ctx)
	if !ok {
		fmt.Println("ParseAccessJwtTokenFromContext 1")
		return errors.New("no jwt token in context")
	}
	if err := su.ParseAccessJwtToken(claims); err != nil {
		fmt.Println("ParseAccessJwtTokenFromContext 2")
		return err
	}
	return nil
}

func (su *SecurityUser) ParseAccessJwtTokenFromString(token string, secretKey []byte) error {
	parseAuth, err := jwtV5.Parse(token, func(*jwtV5.Token) (interface{}, error) {
		return secretKey, nil
	})
	if err != nil {
		return err
	}
	claims, ok := parseAuth.Claims.(jwtV5.MapClaims)
	if !ok {
		return errors.New("claims is not map claims")
	}
	return su.ParseAccessJwtToken(claims)
}

func (su *SecurityUser) ParseAccessJwtToken(claims jwtV5.Claims) error {
	if claims == nil {
		return errors.New("claims is nil")
	}
	mc, ok := claims.(jwtV5.MapClaims)
	if !ok {
		return errors.New("claims is not map claims")
	}
	if v, ok := mc[ClaimAuthorityId].(string); ok {
		su.AuthorityId = v
	}
	if v, ok := mc["jti"].(string); ok && su.AuthorityId == "" {
		su.AuthorityId = v
	}
	return nil
}
