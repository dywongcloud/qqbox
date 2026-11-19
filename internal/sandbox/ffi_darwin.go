//go:build darwin

package sandbox

// FFI into libSystem via goffi (libffi), replacing purego. goffi uses an
// explicit call interface (CIF): each function is resolved to a symbol and
// prepared once with its return and argument types, then invoked by passing
// pointers to the argument values. Helpers here keep the per-function call
// sites small; see chan_darwin.go / hostinfo_darwin.go / termios_darwin.go.

import (
	"unsafe"

	"github.com/go-webgpu/goffi/ffi"
	"github.com/go-webgpu/goffi/types"
)

// Shorthands for the type descriptors qqbox uses (all libSystem calls here
// take and return integers, pointers, or void).
var (
	ffiPtr  = types.PointerTypeDescriptor
	ffiU64  = types.UInt64TypeDescriptor
	ffiU32  = types.UInt32TypeDescriptor
	ffiS32  = types.SInt32TypeDescriptor
	ffiS16  = types.SInt16TypeDescriptor
	ffiVoid = types.VoidTypeDescriptor
)

// ffiCall is a prepared call interface bound to a resolved symbol.
type ffiCall struct {
	cif types.CallInterface
	fn  unsafe.Pointer
}

// libSystem is the one library qqbox reaches through FFI.
func ffiOpenLibSystem() (unsafe.Pointer, error) {
	return ffi.LoadLibrary("/usr/lib/libSystem.B.dylib")
}

// ffiPrep resolves name in lib and prepares its CIF with the given return and
// argument type descriptors.
func ffiPrep(lib unsafe.Pointer, name string, ret *types.TypeDescriptor, args ...*types.TypeDescriptor) (*ffiCall, error) {
	fn, err := ffi.GetSymbol(lib, name)
	if err != nil {
		return nil, err
	}
	c := &ffiCall{fn: fn}
	if err := ffi.PrepareCallInterface(&c.cif, types.DefaultCall, ret, args); err != nil {
		return nil, err
	}
	return c, nil
}

// call invokes the function. ret points to the return slot (nil for void); each
// arg is a pointer to the corresponding argument value. The caller must keep the
// argument values alive across the call (they are on its stack, which goffi
// reads through the pointers).
func (c *ffiCall) call(ret unsafe.Pointer, args ...unsafe.Pointer) {
	ffi.CallFunction(&c.cif, c.fn, ret, args)
}
