package authz

// Role keys for the initial roles (plan §5.1). Code checks permissions, never these keys,
// except for bootstrap and protected-role assignment rules.
const (
	RoleCustomer   = "customer"
	RoleProvider   = "provider"
	RoleSupport    = "support"
	RoleModerator  = "moderator"
	RoleAdmin      = "administrator"
	RoleSuperAdmin = "super_administrator"
)

// RoleDef is a system role with its documented initial permission set.
type RoleDef struct {
	Key         string
	Name        string
	Description string
	Protected   bool // only super administrators may assign or edit
	AllPerms    bool // always holds every catalogue permission (super administrator only)
	Permissions []string
}

// SystemRoles is the seeded role set. Grants are applied only when a role is first created;
// afterwards role-to-permission assignments are data managed by administrators (§5.6).
// The administrator role is deliberately NOT given every permission (§5.2 least privilege):
// it lacks role/permission management, sensitive user/audit data, exports, maintenance
// and feature-flag changes.
var SystemRoles = []RoleDef{
	{
		Key: RoleCustomer, Name: "Customer", Description: "Own profile, favourites, enquiries and eligible reviews",
		Permissions: []string{
			"inquiries.view_own", "inquiries.create", "inquiries.update_own",
			"reviews.view", "reviews.create", "reports.create",
			"jobs.view_own", "notifications.view_own",
		},
	},
	{
		Key: RoleProvider, Name: "Provider", Description: "Own provider profile, services, coverage, media, enquiries and analytics",
		Permissions: []string{
			"providers.view", "providers.create", "providers.update_own", "providers.publish_own", "providers.unpublish_own",
			"provider_services.view_own", "provider_services.create_own", "provider_services.update_own", "provider_services.delete_own",
			"provider_areas.view_own", "provider_areas.manage_own",
			"media.view_own", "media.upload_own", "media.delete_own",
			"inquiries.view_own", "inquiries.update_own",
			"jobs.view_own", "jobs.update_own",
			"reviews.view", "reports.create", "analytics.view_own", "notifications.view_own",
		},
	},
	{
		Key: RoleSupport, Name: "Support", Description: "Limited user and enquiry lookup",
		Permissions: []string{
			"dashboard.view", "users.view", "users.view_any",
			"providers.view", "providers.view_any", "inquiries.view_any",
			"categories.view", "services.view", "locations.view", "demand_requests.view",
		},
	},
	{
		Key: RoleModerator, Name: "Moderator", Description: "Provider, review, report and media moderation",
		Permissions: []string{
			"dashboard.view", "providers.view", "providers.view_any", "providers.publish_any", "providers.suspend", "providers.restore",
			"reviews.view", "reviews.view_any", "reviews.moderate", "reviews.hide", "reviews.restore",
			"reports.view", "reports.assign", "reports.resolve", "reports.dismiss",
			"moderation.actions.view", "moderation.actions.create", "media.moderate",
		},
	},
	{
		Key: RoleAdmin, Name: "Administrator", Description: "Explicitly assigned administrative permissions",
		Permissions: []string{
			"dashboard.view", "dashboard.view_admin_metrics", "platform.settings.view", "platform.settings.update",
			"platform.feature_flags.view",
			"users.view", "users.view_any", "users.create", "users.update", "users.suspend", "users.restore",
			"users.delete_request.review", "users.roles.assign", "users.sessions.revoke",
			"roles.view", "permissions.view",
			"providers.view", "providers.view_any", "providers.create", "providers.update_any", "providers.publish_any",
			"providers.suspend", "providers.restore", "providers.verify", "providers.reject_verification",
			"providers.assign_featured", "providers.remove_featured",
			"provider_services.manage_any", "provider_areas.manage_any",
			"categories.view", "categories.create", "categories.update", "categories.publish", "categories.archive",
			"services.view", "services.create", "services.update", "services.publish", "services.archive",
			"locations.view", "locations.create", "locations.update", "locations.archive", "locations.manage_aliases",
			"locations.merge", "locations.manage_coordinates",
			"inquiries.view_any", "inquiries.close", "jobs.view_any",
			"reviews.view", "reviews.view_any", "reviews.moderate", "reviews.hide", "reviews.restore",
			"reports.view", "reports.assign", "reports.resolve", "reports.dismiss",
			"moderation.actions.view", "moderation.actions.create",
			"media.view_private", "media.moderate", "media.delete_any",
			"seo.view", "seo.update_defaults", "seo.update_pages", "seo.manage_redirects", "seo.manage_sitemap_rules",
			"pages.view", "pages.create", "pages.update", "pages.publish", "pages.archive",
			"navigation.view", "navigation.update",
			"notifications.view_any", "notification_templates.view", "notification_templates.create",
			"notification_templates.update", "notification_templates.publish", "notification_channels.view",
			"analytics.view_provider", "analytics.view_platform", "audit_logs.view",
			"demand_requests.view", "demand_requests.update",
		},
	},
	{
		Key: RoleSuperAdmin, Name: "Super administrator", Description: "Break-glass access to high-impact platform and authorization controls",
		Protected: true, AllPerms: true,
	},
}

// RoleDefByKey returns the definition of a system role.
func RoleDefByKey(key string) (RoleDef, bool) {
	for _, r := range SystemRoles {
		if r.Key == key {
			return r, true
		}
	}
	return RoleDef{}, false
}
