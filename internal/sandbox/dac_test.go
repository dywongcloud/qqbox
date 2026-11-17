package sandbox

import "testing"

func TestDacPermits(t *testing.T) {
	root := &dacCred{caps: 1<<capDacOverride | 1<<capDacReadSearch | 1<<capFowner}
	user := &dacCred{uid: 1000, gid: 1000, groups: []uint32{10}}
	reader := &dacCred{uid: 1000, gid: 1000, caps: 1 << capDacReadSearch}
	cases := []struct {
		name               string
		c                  *dacCred
		owner, group, mode uint32
		dir                bool
		want               int
		ok                 bool
	}{
		{"root reads 0600", root, 0, 0, 0o600, false, mayRead, true},
		{"root writes 0444", root, 0, 0, 0o444, false, mayWrite, true},
		{"root exec without any x", root, 0, 0, 0o644, false, mayExec, false},
		{"root exec with other x", root, 0, 0, 0o701, false, mayExec, true},
		{"root writes 0555 dir", root, 0, 0, 0o555, true, mayWrite | mayExec, true},
		{"user reads root 0600", user, 0, 0, 0o600, false, mayRead, false},
		{"user writes root 0644", user, 0, 0, 0o644, false, mayWrite, false},
		{"user reads root 0644", user, 0, 0, 0o644, false, mayRead, true},
		{"user mkdir in root 0755", user, 0, 0, 0o755, true, mayWrite | mayExec, false},
		{"user mkdir in 1777", user, 0, 0, 0o1777, true, mayWrite | mayExec, true},
		{"owner class wins over other", user, 1000, 0, 0o607, false, mayRead, true},
		{"owner class denies even if other allows", user, 1000, 0, 0o007, false, mayRead, false},
		{"supplementary group", user, 0, 10, 0o040, false, mayRead, true},
		{"read-search reads", reader, 0, 0, 0o000, false, mayRead, true},
		{"read-search no write", reader, 0, 0, 0o000, false, mayWrite, false},
		{"read-search lists dir", reader, 0, 0, 0o000, true, mayRead | mayExec, true},
	}
	for _, tc := range cases {
		if got := dacPermits(tc.c, tc.owner, tc.group, tc.mode, tc.dir, tc.want); got != tc.ok {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.ok)
		}
	}
	if dacStickyOK(user, 0, 0o1777, 0) {
		t.Error("user removed root's file from a sticky dir")
	}
	if !dacStickyOK(user, 0, 0o1777, 1000) || !dacStickyOK(root, 0, 0o1777, 1000) {
		t.Error("sticky dir refused the owner or CAP_FOWNER")
	}
}
