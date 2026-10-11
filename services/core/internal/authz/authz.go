// Package authz holds the RBAC model (plan §4: owner | admin | approver | viewer).
package authz

import (
	"context"

	"github.com/google/uuid"
)

type Role string

const (
	Owner    Role = "owner"
	Admin    Role = "admin"
	Approver Role = "approver"
	Viewer   Role = "viewer"
)

var rank = map[Role]int{Viewer: 1, Approver: 2, Admin: 3, Owner: 4}

func (r Role) Valid() bool { return rank[r] > 0 }

// AtLeast reports whether r grants everything min grants.
func (r Role) AtLeast(min Role) bool { return rank[r] >= rank[min] && rank[r] > 0 }

// CanGrant reports whether a member with role r may invite someone as target.
// Nobody can mint owners by invitation, and nobody can grant above themselves.
func (r Role) CanGrant(target Role) bool {
	return target != Owner && target.Valid() && r.AtLeast(Admin) && r.AtLeast(target)
}

// Principal is the authenticated user, as asserted by the edge after JWT
// verification (the Function URL's AWS_IAM auth guarantees the caller is the edge).
type Principal struct {
	UserID uuid.UUID
	Email  string
}

// Scope is a principal acting inside one organization with a verified role.
type Scope struct {
	Principal
	Org  uuid.UUID
	Role Role
}

type ctxKey int

const (
	principalKey ctxKey = iota
	scopeKey
)

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey, s)
}

func ScopeFrom(ctx context.Context) Scope { return ctx.Value(scopeKey).(Scope) }
