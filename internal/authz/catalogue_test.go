package authz

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/alumasinde/profy/internal/shared/reqctx"
)

var keyPattern = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+)+$`)

func TestCatalogueKeysUniqueAndWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Catalogue {
		if seen[p.Key] {
			t.Errorf("duplicate permission %s", p.Key)
		}
		seen[p.Key] = true
		if !keyPattern.MatchString(p.Key) {
			t.Errorf("malformed permission key %q", p.Key)
		}
		if p.Key == "admin.access" || strings.HasSuffix(p.Key, ".all") {
			t.Errorf("broad permission %q is not allowed (§5.3)", p.Key)
		}
	}
	// Plan §5.4 lists 125; Phase 5 adds demand_requests.view and demand_requests.update.
	if len(Catalogue) != 127 {
		t.Errorf("catalogue has %d permissions; want 127", len(Catalogue))
	}
}

func TestRoleGrantsAreKnown(t *testing.T) {
	for _, r := range SystemRoles {
		for _, k := range r.Permissions {
			if !Known(k) {
				t.Errorf("role %s grants unknown %s", r.Key, k)
			}
		}
	}
}

func TestAdministratorIsNotOmnipotent(t *testing.T) {
	def, _ := RoleDefByKey(RoleAdmin)
	if def.AllPerms || len(def.Permissions) >= len(Catalogue) {
		t.Fatal("administrator must not hold every permission (§5.2)")
	}
	for _, forbidden := range []string{"roles.permissions.assign", "roles.create", "audit_logs.view_sensitive",
		"users.view_sensitive", "platform.maintenance.enable", "platform.feature_flags.update"} {
		for _, k := range def.Permissions {
			if k == forbidden {
				t.Errorf("administrator must not hold %s by default", forbidden)
			}
		}
	}
	if sa, _ := RoleDefByKey(RoleSuperAdmin); !sa.AllPerms || !sa.Protected {
		t.Fatal("super administrator must be protected and complete")
	}
}

func TestCanFailsClosed(t *testing.T) {
	s := New(nil, 0) // nil DB: any lookup would panic, so these must short-circuit
	if s.Can(context.Background(), nil, "users.view") {
		t.Fatal("nil principal allowed")
	}
	if s.Can(context.Background(), &reqctx.Principal{UserID: 1}, "not.a.permission") {
		t.Fatal("unknown permission allowed")
	}
	if s.CanOwnOrAny(context.Background(), nil, "providers.update", 1) {
		t.Fatal("nil principal allowed for ownership check")
	}
}

func TestRequirePanicsOnUnknownPermission(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown permission")
		}
	}()
	New(nil, 0).Require("made.up")
}
