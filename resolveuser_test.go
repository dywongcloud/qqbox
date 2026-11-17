package laboon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/samyfodil/laboon/internal/sandbox"
)

func TestResolveUser(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := "root:x:0:0:root:/root:/bin/sh\nchrome:x:1000:1000:chrome:/home/chrome:/sbin/nologin\n"
	group := "root:x:0:\nchrome:x:1000:\naudio:x:29:chrome\nvideo:x:28:chrome\n"
	os.WriteFile(filepath.Join(root, "etc/passwd"), []byte(passwd), 0o644)
	os.WriteFile(filepath.Join(root, "etc/group"), []byte(group), 0o644)

	cases := []struct {
		spec string
		want *sandbox.UserID
	}{
		{"", nil},     // root
		{"root", nil}, // resolves to 0:0 -> root
		{"chrome", &sandbox.UserID{Uid: 1000, Gid: 1000, Groups: []uint32{29, 28}}},
		{"1000", &sandbox.UserID{Uid: 1000, Gid: 1000}},                             // numeric, gid defaults to uid
		{"1000:2000", &sandbox.UserID{Uid: 1000, Gid: 2000}},                        // explicit numeric gid
		{"chrome:audio", &sandbox.UserID{Uid: 1000, Gid: 29, Groups: []uint32{28}}}, // group override; 29 no longer supplementary
	}
	for _, c := range cases {
		got, err := resolveUser(root, c.spec)
		if err != nil {
			t.Errorf("resolveUser(%q): %v", c.spec, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("resolveUser(%q) = %+v, want %+v", c.spec, got, c.want)
		}
	}

	if _, err := resolveUser(root, "ghost"); err == nil {
		t.Errorf("resolveUser(ghost): want error for unknown user")
	}
}
