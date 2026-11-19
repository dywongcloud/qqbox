//go:build windows

package sandbox

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/tencent/qqbox/internal/rewrite"
	winstub "github.com/tencent/qqbox/stub/windows"
	"golang.org/x/sys/windows"
)

// A rewritten image is kept on disk, and later loads map its read-only part
// (the trampolines and every segment below the first writable one) into the
// stub as a view of that file instead of reading, rewriting and copying it
// again; only the writable segments are copied. The view is shared by every
// process that runs the image and kept by a fork child, as the stub's own
// shared mappings are.
//
// An entry is keyed by the file's identity, the stub's entry points and this
// supervisor's own executable, so a rebuilt supervisor or stub never maps code
// another one rewrote. A position-independent image is stored at the bias it
// was first loaded at and moved to each load's (wimage.relocate): its
// trampolines address the guest only relative to the code. Entries are not
// pruned.

type imgView struct {
	sec  windows.Handle
	size uint64 // bytes from the image's lo mapped from the file
}

type imgMeta struct {
	Entry, Phdr, Phnum, Lo, Hi, Base uint64
	Interp                           string
	View, Tail                       uint64
	Prots                            [][3]uint64
	Segs                             [][5]uint64
	Traps                            []uint64
}

var imgDir = sync.OnceValue(func() string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "qqbox", "img")
})

// selfID identifies this supervisor's executable and its stub.
var selfID = sync.OnceValue(func() []byte {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	f, err := os.Open(exe)
	if err != nil {
		return nil
	}
	defer f.Close()
	id, ok := fileIdentity(f)
	if !ok {
		return nil
	}
	s := sha256.Sum256(winstub.Binary)
	return binary.LittleEndian.AppendUint64(append(s[:8], idBytes(id)...), imgCacheVersion)
})

// imgCacheVersion changes when what an entry holds does.
const imgCacheVersion = 2

func idBytes(id rewrite.FileID) []byte {
	b := binary.LittleEndian.AppendUint64(nil, id.Dev)
	b = binary.LittleEndian.AppendUint64(b, id.Ino)
	b = binary.LittleEndian.AppendUint64(b, uint64(id.Size))
	b = binary.LittleEndian.AppendUint64(b, uint64(id.Mtime))
	return binary.LittleEndian.AppendUint64(b, uint64(id.Ctime))
}

// imgPath is the cache entry for src, or "" when it has none.
func (g *wguest) imgPath(src imageSrc) string {
	self := selfID()
	if !src.hasID || self == nil || imgDir() == "" || testNoImgCache {
		return ""
	}
	b := append([]byte(guestArch), self...)
	b = append(b, idBytes(src.id)...)
	for _, v := range []uint64{g.entry, g.exitEntry} {
		b = binary.LittleEndian.AppendUint64(b, v)
	}
	if g.cfg.NoRewrite {
		b = append(b, 'n')
	}
	if strictRewrite {
		b = append(b, 's')
	}
	h := sha256.Sum256(b)
	return filepath.Join(imgDir(), hex.EncodeToString(h[:16]))
}

// cachedImage returns the image stored at path, with its view, or nil.
func cachedImage(path string) *wimage {
	mb, err := os.ReadFile(path + ".json")
	if err != nil {
		return nil
	}
	var m imgMeta
	if json.Unmarshal(mb, &m) != nil || m.View == 0 || m.Lo+m.View > m.Hi || m.Tail > m.Hi-m.Lo-m.View {
		return nil
	}
	fp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}
	h, err := windows.CreateFile(fp, windows.GENERIC_READ|windows.GENERIC_EXECUTE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(h)
	tail := make([]byte, m.Tail)
	if m.Tail > 0 {
		var n uint32
		ov := windows.Overlapped{Offset: uint32(m.View), OffsetHigh: uint32(m.View >> 32)}
		if windows.ReadFile(h, tail, &n, &ov) != nil || uint64(n) != m.Tail {
			return nil
		}
	}
	sec, err := windows.CreateFileMapping(h, nil, windows.PAGE_EXECUTE_READ, uint32(m.View>>32), uint32(m.View), nil)
	if err != nil {
		return nil
	}
	img := &wimage{entry: m.Entry, phdr: m.Phdr, phnum: m.Phnum, lo: m.Lo, hi: m.Hi, base: m.Base, interp: m.Interp,
		bytes: tail, view: &imgView{sec: sec, size: m.View}, traps: m.Traps}
	for _, p := range m.Prots {
		img.prots = append(img.prots, wrun{p[0], p[1], uint32(p[2])})
	}
	for _, s := range m.Segs {
		img.segs = append(img.segs, mapEnt{s[0], s[1], s[2], s[3], s[4]})
	}
	return img
}

// saveImage stores img, prepared by loadELF with its trampolines at its lo
// and no read-only segment past view, so later loads can map it. A failure
// only costs those loads the work.
func saveImage(path string, img *wimage, view uint64) {
	if path == "" || view == 0 || view > img.hi-img.lo {
		return
	}
	tail := img.bytes[view:]
	for len(tail) > 0 && tail[len(tail)-1] == 0 {
		tail = tail[:len(tail)-1]
	}
	m := imgMeta{Entry: img.entry, Phdr: img.phdr, Phnum: img.phnum, Lo: img.lo, Hi: img.hi, Base: img.base,
		Interp: img.interp, View: view, Tail: uint64(len(tail)), Traps: img.traps}
	for _, p := range img.prots {
		m.Prots = append(m.Prots, [3]uint64{p.lo, p.hi, uint64(p.prot)})
	}
	for _, s := range img.segs {
		m.Segs = append(m.Segs, [5]uint64{s.lo, s.hi, s.off, s.aux, s.attr})
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	// The data first, the metadata last: an entry is only used whole.
	if writeFileAtomic(path, img.bytes[:view], tail) != nil {
		return
	}
	writeFileAtomic(path+".json", mb, nil)
}

func writeFileAtomic(path string, a, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".img-*")
	if err != nil {
		return err
	}
	_, err = f.Write(a)
	if err == nil {
		_, err = f.Write(b)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

var (
	modNtdll                      = windows.NewLazySystemDLL("ntdll.dll")
	procNtAllocateVirtualMemoryEx = modNtdll.NewProc("NtAllocateVirtualMemoryEx")
	procNtFreeVirtualMemory       = modNtdll.NewProc("NtFreeVirtualMemory")
	procNtMapViewOfSectionEx      = modNtdll.NewProc("NtMapViewOfSectionEx")
)

// testNoImgCache makes every load rewrite and copy its image.
var testNoImgCache bool

const (
	memReservePlaceholder  = 0x40000
	memReplacePlaceholder  = 0x4000
	memPreservePlaceholder = 0x2
)

// mapImageView installs img's view and copies its writable tail: the range
// is reserved as a placeholder, split at the view's end and both parts
// replaced.
func (g *wguest) mapImageView(img *wimage) error {
	defer windows.CloseHandle(img.view.sec)
	proc := uintptr(g.sp.proc)
	lo, mid, hi := img.lo, img.lo+img.view.size, img.hi
	base, size := uintptr(lo), uintptr(hi-lo)
	if st, _, _ := procNtAllocateVirtualMemoryEx.Call(proc, uintptr(unsafe.Pointer(&base)), uintptr(unsafe.Pointer(&size)),
		windows.MEM_RESERVE|memReservePlaceholder, windows.PAGE_NOACCESS, 0, 0); st != 0 {
		return ntErr("reserve image", st)
	}
	if mid < hi {
		base, size = uintptr(lo), uintptr(mid-lo)
		if st, _, _ := procNtFreeVirtualMemory.Call(proc, uintptr(unsafe.Pointer(&base)), uintptr(unsafe.Pointer(&size)),
			windows.MEM_RELEASE|memPreservePlaceholder); st != 0 {
			return ntErr("split image", st)
		}
	}
	base, size = uintptr(lo), uintptr(mid-lo)
	var off int64
	if st, _, _ := procNtMapViewOfSectionEx.Call(uintptr(img.view.sec), proc, uintptr(unsafe.Pointer(&base)),
		uintptr(unsafe.Pointer(&off)), uintptr(unsafe.Pointer(&size)), memReplacePlaceholder, windows.PAGE_EXECUTE_READ, 0, 0); st != 0 {
		return ntErr("map image", st)
	}
	if mid < hi {
		base, size = uintptr(mid), uintptr(hi-mid)
		if st, _, _ := procNtAllocateVirtualMemoryEx.Call(proc, uintptr(unsafe.Pointer(&base)), uintptr(unsafe.Pointer(&size)),
			windows.MEM_RESERVE|windows.MEM_COMMIT|memReplacePlaceholder, windows.PAGE_READWRITE, 0, 0); st != 0 {
			return ntErr("image data", st)
		}
		if err := g.sp.writeMem(mid, img.bytes); err != nil {
			return err
		}
		if err := g.sp.protect(mid, hi-mid, windows.PAGE_NOACCESS); err != nil {
			return err
		}
	}
	if err := g.sp.flushCode(lo, mid-lo); err != nil {
		return err
	}
	if err := g.sp.protect(lo, mid-lo, windows.PAGE_NOACCESS); err != nil {
		return err
	}
	for _, r := range img.prots {
		if err := g.sp.protect(r.lo, r.hi-r.lo, r.prot); err != nil {
			return err
		}
	}
	return nil
}

func ntErr(what string, st uintptr) error {
	return &os.SyscallError{Syscall: what, Err: windows.NTStatus(st)}
}
