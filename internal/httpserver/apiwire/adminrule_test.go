package apiwire

import "testing"

// TestAdminRule_TheWiredRuleDecides: with the platform's admin rule wired
// (#2228) it decides; without it, holding an admin role does.
func TestAdminRule_TheWiredRuleDecides(t *testing.T) {
	ruled := Deps{AdminRoles: []string{"admin"}, IsAdmin: func(roles []string) bool { return len(roles) == 0 }}
	if ruled.adminRule()([]string{"admin"}) || !ruled.adminRule()(nil) {
		t.Error("the wired rule did not decide")
	}
	if !(Deps{AdminRoles: []string{"admin"}}).adminRule()([]string{"admin"}) {
		t.Error("with no rule, the admin role no longer decides")
	}
}
