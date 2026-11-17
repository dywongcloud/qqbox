//go:build darwin || linux

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// cachedFS remembers lookups so a path walk costs no host calls once its
// components have been seen. The lower layer is read-only, so its lstat and
// readlink answers hold for the whole run. The upper layer changes only through
// the supervisor; the paths found missing there are remembered until the next
// change to it, and a path below a missing directory is missing too, and so is
// what a lookup reads of a path found there (its kind and inode, which only a
// change to the layer's names alters). Without a root, guest paths are host
// paths anyone may change, and nothing is cached.
type cachedFS struct {
	hostFS
	lower, upper string   // layer roots with a trailing separator, "" if none
	own          []string // the supervisor's writable directories, should the root hold them

	mu     sync.Mutex
	stats  map[string]lstatResult
	links  map[string]linkResult
	absent map[string]bool     // upper paths that did not exist
	ids    map[string]idResult // upper paths found, for a lookup
	gen    uint64              // upper layer changes; a lookup older than one is not kept
}

type idResult struct {
	fi  os.FileInfo
	gen uint64
}

type lstatResult struct {
	fi  os.FileInfo
	err error
}

type linkResult struct {
	target string
	err    error
}

// cacheMax bounds each table; a full one starts over.
const cacheMax = 1 << 17

func newCachedFS(h hostFS, rootAbs, upperAbs string) hostFS {
	c := &cachedFS{hostFS: h}
	if rootAbs == "" {
		return h
	}
	c.lower = withSep(rootAbs)
	if upperAbs != "" {
		c.upper = withSep(upperAbs)
	}
	if d := tmpDir(); d != "" {
		c.own = append(c.own, withSep(d))
	}
	c.own = append(c.own, "/tmp/lcn.")
	return c
}

// withSep is dir with one trailing separator (the root "/" has it already).
func withSep(dir string) string {
	if strings.HasSuffix(dir, string(filepath.Separator)) {
		return dir
	}
	return dir + string(filepath.Separator)
}

// inLower reports whether p is in the read-only lower layer. A root of /
// holds the upper layer and the supervisor's own directories too.
func (c *cachedFS) inLower(p string) bool {
	if !strings.HasPrefix(p, c.lower) || c.inUpper(p) {
		return false
	}
	for _, d := range c.own {
		if strings.HasPrefix(p, d) {
			return false
		}
	}
	return true
}

func (c *cachedFS) inUpper(p string) bool { return c.upper != "" && strings.HasPrefix(p, c.upper) }

// missing reports whether upper path p, or a directory above it, was found
// missing since the last change to the upper layer.
func (c *cachedFS) missing(p string) bool {
	for q := p; len(q) >= len(c.upper); q = filepath.Dir(q) {
		if c.absent[q] {
			return true
		}
	}
	return false
}

func (c *cachedFS) hLstat(p string) (os.FileInfo, error) {
	switch {
	case c.inLower(p):
		c.mu.Lock()
		r, ok := c.stats[p]
		c.mu.Unlock()
		if ok {
			return r.fi, r.err
		}
		fi, err := c.hostFS.hLstat(p)
		c.mu.Lock()
		if c.stats == nil || len(c.stats) >= cacheMax {
			c.stats = map[string]lstatResult{}
		}
		c.stats[p] = lstatResult{fi, err}
		c.mu.Unlock()
		return fi, err
	case c.inUpper(p):
		c.mu.Lock()
		gone := c.missing(p)
		gen := c.gen
		c.mu.Unlock()
		if gone {
			return nil, &os.PathError{Op: "lstat", Path: p, Err: syscall.ENOENT}
		}
		fi, err := c.hostFS.hLstat(p)
		if pe, ok := err.(*os.PathError); ok && pe.Err == syscall.ENOENT {
			c.mu.Lock()
			if c.gen == gen {
				if c.absent == nil || len(c.absent) >= cacheMax {
					c.absent = map[string]bool{}
				}
				c.absent[p] = true
			}
			c.mu.Unlock()
		}
		return fi, err
	}
	return c.hostFS.hLstat(p)
}

// hLstatID is an lstat for a lookup, which reads only the kind and the device
// and inode of the result.
func (c *cachedFS) hLstatID(p string) (os.FileInfo, error) {
	if !c.inUpper(p) {
		return c.hLstat(p)
	}
	c.mu.Lock()
	r, ok := c.ids[p]
	gen := c.gen
	c.mu.Unlock()
	if ok && r.gen == gen {
		return r.fi, nil
	}
	fi, err := c.hLstat(p)
	if err == nil {
		c.mu.Lock()
		if c.gen == gen {
			if c.ids == nil || len(c.ids) >= cacheMax {
				c.ids = map[string]idResult{}
			}
			c.ids[p] = idResult{fi, gen}
		}
		c.mu.Unlock()
	}
	return fi, err
}

func (c *cachedFS) hReadlink(p string) (string, error) {
	if !c.inLower(p) {
		return c.hostFS.hReadlink(p)
	}
	c.mu.Lock()
	r, ok := c.links[p]
	c.mu.Unlock()
	if ok {
		return r.target, r.err
	}
	// Most components are not links: the lstat, cached for the lookups
	// that follow, says so without the readlink's open of the parent.
	var t string
	fi, err := c.hLstat(p)
	switch {
	case err != nil:
		if pe, ok := err.(*os.PathError); ok {
			err = &os.PathError{Op: "readlink", Path: p, Err: pe.Err}
		}
	case fi.Mode()&os.ModeSymlink == 0:
		err = &os.PathError{Op: "readlink", Path: p, Err: syscall.EINVAL}
	default:
		t, err = c.hostFS.hReadlink(p)
	}
	c.mu.Lock()
	if c.links == nil || len(c.links) >= cacheMax {
		c.links = map[string]linkResult{}
	}
	c.links[p] = linkResult{t, err}
	c.mu.Unlock()
	return t, err
}

// changed forgets the upper layer's missing paths once a change to it is
// done: something may exist there now.
func (c *cachedFS) changed() {
	c.mu.Lock()
	c.gen++
	clear(c.absent)
	c.mu.Unlock()
}

func (c *cachedFS) hOpen(p string, flag int, perm os.FileMode) (*os.File, error) {
	if flag&os.O_CREATE != 0 {
		defer c.changed()
	}
	return c.hostFS.hOpen(p, flag, perm)
}

func (c *cachedFS) hMkdir(p string, perm uint32) error {
	defer c.changed()
	return c.hostFS.hMkdir(p, perm)
}

func (c *cachedFS) hSymlink(target, p string) error {
	defer c.changed()
	return c.hostFS.hSymlink(target, p)
}

func (c *cachedFS) hMkfifo(p string, perm uint32) error {
	defer c.changed()
	return c.hostFS.hMkfifo(p, perm)
}

func (c *cachedFS) hMksock(p string, perm uint32) error {
	defer c.changed()
	return c.hostFS.hMksock(p, perm)
}

func (c *cachedFS) hRename(from, to string, exchange bool) error {
	defer c.changed()
	return c.hostFS.hRename(from, to, exchange)
}

func (c *cachedFS) hUnlink(p string, dir bool) error {
	defer c.changed()
	return c.hostFS.hUnlink(p, dir)
}

func (c *cachedFS) hLink(from, to string) error {
	defer c.changed()
	return c.hostFS.hLink(from, to)
}

func (c *cachedFS) hClone(from, to string) error {
	defer c.changed()
	return c.hostFS.hClone(from, to)
}

func (c *cachedFS) hCloneFile(f *os.File, to string) error {
	defer c.changed()
	return c.hostFS.hCloneFile(f, to)
}

// upperChanged tells the lookup cache that something outside hostFS created
// a file in the upper layer (a unix socket's bind).
func (fs *dfs) upperChanged() {
	if c, ok := fs.hostFS.(*cachedFS); ok {
		c.changed()
	}
}
