package middleware

import (
	"context"
	"server/app/admin/internal/service"

	"github.com/casbin/casbin/v2"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	_ "github.com/go-sql-driver/mysql"
)

type contextKey string

const (
	ModelContextKey        contextKey = "CasbinModel"
	PolicyContextKey       contextKey = "CasbinPolicy"
	EnforcerContextKey     contextKey = "CasbinEnforcer"
	SecurityUserContextKey contextKey = "CasbinSecurityUser"

	reason string = "FORBIDDEN"
)

var (
	ErrSecurityUserCreatorMissing = errors.Forbidden(reason, "SecurityUserCreator is required")
	ErrEnforcerMissing            = errors.Forbidden(reason, "Enforcer is missing")
	ErrSecurityParseFailed        = errors.Forbidden(reason, "Security Info fault")
	ErrUnauthorized               = errors.Forbidden(reason, "Unauthorized Access")
)

type Option func(*CasbinZ)

var CasbinInstance *CasbinZ

type CasbinZ struct {
	enableDomain        bool
	source              string
	modefile            string
	securityUserCreator SecurityUserCreator
	adapter             *gormadapter.Adapter
	Enforcer            *casbin.SyncedEnforcer
	adminService        *service.AdminService
	superid             string
}

// param[0] = sql source
// param[1] = model file path
func NewCasbinZ(service *service.AdminService, super string, param ...string) (*CasbinZ, error) {
	if CasbinInstance != nil {
		return CasbinInstance, nil
	}
	a, err := gormadapter.NewAdapter("mysql", param[0], true)
	if err != nil {
		return nil, err
	}
	e, err := casbin.NewSyncedEnforcer(param[1], a)
	if err != nil {
		return nil, err
	}

	CasbinInstance = &CasbinZ{
		source:       param[0],
		modefile:     param[1],
		adapter:      a,
		Enforcer:     e,
		adminService: service,
		superid:      super,
	}
	return CasbinInstance, nil
}

// WithDomainSupport  enable domain support
func WithDomainSupport() Option {
	return func(o *CasbinZ) {
		o.enableDomain = true
	}
}

func WithSecurityUserCreator(securityUserCreator SecurityUserCreator) Option {
	return func(o *CasbinZ) {
		o.securityUserCreator = securityUserCreator
	}
}

func (c *CasbinZ) Server(opts ...Option) middleware.Middleware {

	for _, opt := range opts {
		opt(c)
	}

	c.Enforcer, _ = casbin.NewSyncedEnforcer(c.modefile, c.adapter)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (interface{}, error) {
			var (
				allowed bool
				err     error
			)

			if c.Enforcer == nil {
				return nil, ErrEnforcerMissing
			}
			if c.securityUserCreator == nil {
				return nil, ErrSecurityUserCreatorMissing
			}

			securityUser := c.securityUserCreator()
			if err := securityUser.ParseFromContext(ctx); err != nil {
				return nil, ErrSecurityParseFailed
			}

			ctx = context.WithValue(ctx, SecurityUserContextKey, securityUser)
			// 判断是否是超级用户
			if c.superid == securityUser.GetSubject() {
				return handler(ctx, req)
			}
			///
			Obj := c.adminService.GetMenuid(ctx, securityUser.GetObject())
			_ = c.Enforcer.LoadPolicy()
			if c.enableDomain {
				allowed, err = c.Enforcer.Enforce(securityUser.GetSubject(), securityUser.GetDomain(), Obj, securityUser.GetAction())
			} else {
				allowed, err = c.Enforcer.Enforce(securityUser.GetSubject(), Obj, securityUser.GetAction())
			}
			if err != nil {
				return nil, err
			}
			if !allowed {
				return nil, ErrUnauthorized
			}
			return handler(ctx, req)
		}
	}
}

func (c *CasbinZ) Client(opts ...Option) middleware.Middleware {

	for _, opt := range opts {
		opt(c)
	}

	c.Enforcer, _ = casbin.NewSyncedEnforcer(c.modefile, c.adapter)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (interface{}, error) {
			return handler(ctx, req)
		}
	}
}

// SecurityUserFromContext extract SecurityUser from context
func SecurityUserFromContext(ctx context.Context) (SecurityUser, bool) {
	user, ok := ctx.Value(SecurityUserContextKey).(SecurityUser)
	return user, ok
}

func (c *CasbinZ) ClearCasbin(v int, p ...string) (bool, error) {
	return c.Enforcer.RemoveFilteredPolicy(v, p...)
}

// rules = { {id,path,method},{id,path,method} }
func (c *CasbinZ) AddPolicies(rules [][]string) (bool, error) {
	return c.Enforcer.AddPolicies(rules)
}
