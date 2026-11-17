package laboon

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samyfodil/laboon/internal/sandbox"
)

// resolveUser turns an image's USER ("", a name, uid, uid:gid or name:group)
// into the credentials the first process runs as, reading /etc/passwd and
// /etc/group from the rootfs to resolve names and a user's supplementary
// groups, as docker run does. "" (or root) returns nil, which runs as root.
func resolveUser(root, spec string) (*sandbox.UserID, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	userPart, groupPart, hasGroup := strings.Cut(spec, ":")

	u := &sandbox.UserID{}
	var name string
	gidKnown := false
	if n, err := strconv.ParseUint(userPart, 10, 32); err == nil {
		u.Uid = uint32(n)
	} else {
		pw, ok := passwdByName(root, userPart)
		if !ok {
			return nil, &userError{spec, "no such user " + userPart}
		}
		u.Uid, u.Gid, name, gidKnown = pw.uid, pw.gid, pw.name, true
	}

	if hasGroup {
		if n, err := strconv.ParseUint(groupPart, 10, 32); err == nil {
			u.Gid, gidKnown = uint32(n), true
		} else if gid, ok := groupGID(root, groupPart); ok {
			u.Gid, gidKnown = gid, true
		} else {
			return nil, &userError{spec, "no such group " + groupPart}
		}
	}
	if !gidKnown {
		// A numeric uid with no group runs with that gid, as docker does.
		u.Gid = u.Uid
	}
	if name != "" {
		u.Groups = supplementaryGroups(root, name, u.Gid)
	}
	if u.Uid == 0 && u.Gid == 0 && len(u.Groups) == 0 {
		return nil, nil // root, with full capabilities
	}
	return u, nil
}

type userError struct{ spec, why string }

func (e *userError) Error() string { return "user " + strconv.Quote(e.spec) + ": " + e.why }

type passwdEnt struct {
	name     string
	uid, gid uint32
}

// passwdByName finds a user in the rootfs /etc/passwd.
func passwdByName(root, name string) (passwdEnt, bool) {
	var ent passwdEnt
	eachLine(filepath.Join(root, "etc/passwd"), func(f []string) bool {
		if len(f) >= 4 && f[0] == name {
			uid, e1 := strconv.ParseUint(f[2], 10, 32)
			gid, e2 := strconv.ParseUint(f[3], 10, 32)
			if e1 == nil && e2 == nil {
				ent = passwdEnt{name: name, uid: uint32(uid), gid: uint32(gid)}
				return false
			}
		}
		return true
	})
	return ent, ent.name != ""
}

// groupGID finds a group's gid in the rootfs /etc/group.
func groupGID(root, name string) (uint32, bool) {
	var gid uint32
	found := false
	eachLine(filepath.Join(root, "etc/group"), func(f []string) bool {
		if len(f) >= 3 && f[0] == name {
			if g, err := strconv.ParseUint(f[2], 10, 32); err == nil {
				gid, found = uint32(g), true
				return false
			}
		}
		return true
	})
	return gid, found
}

// supplementaryGroups collects the groups a user is a member of in /etc/group,
// excluding its primary gid.
func supplementaryGroups(root, name string, primary uint32) []uint32 {
	var groups []uint32
	eachLine(filepath.Join(root, "etc/group"), func(f []string) bool {
		if len(f) < 4 {
			return true
		}
		for _, m := range strings.Split(f[3], ",") {
			if m == name {
				if g, err := strconv.ParseUint(f[2], 10, 32); err == nil && uint32(g) != primary {
					groups = append(groups, uint32(g))
				}
				break
			}
		}
		return true
	})
	return groups
}

// eachLine calls fn with the colon-split fields of each line of a passwd/group
// file, stopping when fn returns false. A missing file is not an error: an
// image may name a numeric id with no such file.
func eachLine(path string, fn func(fields []string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !fn(strings.Split(line, ":")) {
			return
		}
	}
}
