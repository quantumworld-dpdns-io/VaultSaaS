package authz

import "testing"

func TestRoleOrdering(t *testing.T) {
	if !Owner.AtLeast(Admin) || !Admin.AtLeast(Approver) || !Approver.AtLeast(Viewer) {
		t.Fatal("hierarchy broken")
	}
	if Viewer.AtLeast(Approver) || Role("root").AtLeast(Viewer) || Role("").AtLeast(Viewer) {
		t.Fatal("unknown or lower roles must not pass")
	}
}

func TestCanGrant(t *testing.T) {
	cases := []struct {
		who, target Role
		ok          bool
	}{
		{Owner, Admin, true}, {Owner, Owner, false}, {Admin, Admin, true}, {Admin, Viewer, true},
		{Approver, Viewer, false}, {Viewer, Viewer, false}, {Admin, Role("superadmin"), false},
	}
	for _, c := range cases {
		if c.who.CanGrant(c.target) != c.ok {
			t.Errorf("%s granting %s: want %v", c.who, c.target, c.ok)
		}
	}
}
