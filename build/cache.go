package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// Cache stores and retrieves built image layers by a content-addressed key, so
// an unchanged build step is reused instead of re-run. It is swappable: a build
// can use the disk-backed DirCache, an in-memory cache, or any store (a shared
// remote, say) that satisfies this interface. Implementations must be safe for
// use by one build at a time; Get returns false when the key is absent.
type Cache interface {
	Get(key string) (v1.Layer, bool)
	Put(key string, layer v1.Layer) error
}

// stepKey chains a step's cache key from the previous key, the instruction and
// any extra content (the copied files' hash for COPY/ADD). Chaining means a
// change to any earlier step, or to this instruction, cascades to a miss here
// and in every step after it, as a layer cache must.
func stepKey(prev string, in Instruction, content string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", prev, canonicalInstr(in), content)
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalInstr is a stable textual form of an instruction for keying: the
// verb, its form, its flags in sorted order and its arguments.
func canonicalInstr(in Instruction) string {
	var b strings.Builder
	b.WriteString(in.Cmd)
	if in.Exec {
		b.WriteString("\x01exec")
	}
	keys := make([]string, 0, len(in.Flags))
	for k := range in.Flags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, " --%s=%s", k, in.Flags[k])
	}
	for _, a := range in.Args {
		b.WriteString(" ")
		b.WriteString(a)
	}
	return b.String()
}

// hashTree writes a stable digest of the file tree at root (a file or a
// directory) into h: each path's relative name, type and mode, plus a regular
// file's bytes or a symlink's target. It is how COPY/ADD factors the source
// content into the cache key, so editing a copied file busts the cache.
func hashTree(h io.Writer, root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%o\x00", filepath.ToSlash(rel), fi.Mode())
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			tgt, err := os.Readlink(p)
			if err != nil {
				return err
			}
			io.WriteString(h, tgt)
		case fi.Mode().IsRegular():
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
		}
		io.WriteString(h, "\n")
		return nil
	})
}

// hashTrees digests the content of a COPY/ADD's source roots into a hex key
// fragment, so editing a copied file changes the step's cache key.
func hashTrees(roots []string) (string, error) {
	h := sha256.New()
	for _, r := range roots {
		if err := hashTree(h, r); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DirCache is a disk-backed Cache: each layer is stored as an uncompressed tar
// named by its key under dir. It is the default build cache.
type DirCache struct{ dir string }

// NewDirCache opens (creating it) a disk cache rooted at dir.
func NewDirCache(dir string) (*DirCache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DirCache{dir: dir}, nil
}

func (c *DirCache) path(key string) string { return filepath.Join(c.dir, key+".tar") }

func (c *DirCache) Get(key string) (v1.Layer, bool) {
	p := c.path(key)
	if _, err := os.Stat(p); err != nil {
		return nil, false
	}
	l, err := tarball.LayerFromFile(p)
	if err != nil {
		return nil, false
	}
	return l, true
}

func (c *DirCache) Put(key string, layer v1.Layer) error {
	rc, err := layer.Uncompressed()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(c.dir, "put-")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), c.path(key))
}
