//go:build darwin

package sandbox

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/go-webgpu/goffi/ffi"
	"github.com/go-webgpu/goffi/types"
)

// broker is how stubs get Mach port rights to one another, which a native
// fork needs: Darwin keeps x18 only for a process it execs, so a fork child
// is a fresh stub, and its parent sends it its memory as Mach memory entries.
// Every stub is started with a send right to the broker's port as its
// bootstrap port and sends it a send right to its own mailbox port before it
// sandboxes itself; the supervisor hands the parent of a fork the child's.
//
// A stub also registers a service port, which only the broker holds: the
// supervisor asks there for what a stub must answer while its guest threads
// run (another process's /proc/<pid>/maps).
type broker struct {
	port uint32 // receive right; a stub's bootstrap special port names it

	mu       sync.Mutex
	cond     *sync.Cond
	boxes    map[int]uint32 // stub pid -> send right to its mailbox
	services map[int]uint32 // stub pid -> send right to its service port
	dead     bool

	code codeReg // shared code across stubs (codereg_darwin.go)
}

const (
	machPortRightReceive = 1
	machMsgTypeMoveSend  = 17
	machMsgTypeCopySend  = 19
	machMsgTypeMakeSend  = 20
	machMsgPortDesc      = 0
	machMsgBitsComplex   = 0x80000000
	machSendMsg          = 1
	machRcvMsg           = 2
	machRcvTrailerAudit  = 3 << 24 // MACH_RCV_TRAILER_ELEMENTS(MACH_RCV_TRAILER_AUDIT)
	machRcvTooLarge      = 0x10004004
	machRcvPortDied      = 0x10004008
	machRcvInvalidName   = 0x10004002

	brokerRegister   = 0x4c430100 // stub -> broker: its mailbox
	brokerChild      = 0x4c430101 // supervisor -> a forking stub: its child's mailbox and pid
	brokerService    = 0x4c430105 // stub -> broker: its service port
	brokerMaps       = 0x4c430110 // supervisor -> a stub's service: its mappings
	brokerMapsReply  = 0x4c430111
	brokerSmaps      = 0x4c430112 // supervisor -> a stub's service: its mappings with their counts
	brokerSmapsReply = 0x4c430113

	machMsgTypeMoveSendOnce = 18
	machMsgTypeMakeSendOnce = 21
)

type machMsgHeader struct {
	bits, size             uint32
	remote, local, voucher uint32
	id                     int32
}

type machPortDescriptor struct {
	name        uint32
	pad1        uint32
	pad2        uint16
	disposition uint8
	typ         uint8
}

// brokerMsg is a message carrying one port: a registration (stub -> broker)
// or a child's mailbox (supervisor -> the forking stub), with a pid.
type brokerMsg struct {
	h    machMsgHeader
	n    uint32
	port machPortDescriptor
	pid  int32
}

var (
	brokerOnce                                               sync.Once
	brokerErr                                                error
	machTask                                                 uint32
	mpAllocate, mpInsertRight, mpDeallocate, mpModRefs, mMsg *ffiCall
	mVmDeallocate, cMemcpy                                   *ffiCall
)

func initBroker() error {
	brokerOnce.Do(func() {
		lib, err := ffiOpenLibSystem()
		if err != nil {
			brokerErr = err
			return
		}
		self, err := ffi.GetSymbol(lib, "mach_task_self_")
		if err != nil {
			brokerErr = err
			return
		}
		machTask = *(*uint32)(self)
		for _, p := range []struct {
			c    **ffiCall
			name string
			args []*types.TypeDescriptor
		}{
			{&mpAllocate, "mach_port_allocate", []*types.TypeDescriptor{ffiU32, ffiU32, ffiPtr}},
			{&mpInsertRight, "mach_port_insert_right", []*types.TypeDescriptor{ffiU32, ffiU32, ffiU32, ffiU32}},
			{&mpDeallocate, "mach_port_deallocate", []*types.TypeDescriptor{ffiU32, ffiU32}},
			{&mpModRefs, "mach_port_mod_refs", []*types.TypeDescriptor{ffiU32, ffiU32, ffiU32, ffiS32}},
			{&mMsg, "mach_msg", []*types.TypeDescriptor{ffiPtr, ffiS32, ffiU32, ffiU32, ffiU32, ffiU32, ffiU32}},
			{&mVmDeallocate, "mach_vm_deallocate", []*types.TypeDescriptor{ffiU32, ffiU64, ffiU64}},
		} {
			if *p.c, brokerErr = ffiPrep(lib, p.name, ffiS32, p.args...); brokerErr != nil {
				return
			}
		}
		// memcpy copies out of memory a message delivered out of line, whose
		// address arrives as a number.
		cMemcpy, brokerErr = ffiPrep(lib, "memcpy", ffiPtr, ffiPtr, ffiU64, ffiU64)
	})
	return brokerErr
}

// newBroker makes the broker's port and starts taking registrations.
func newBroker() (*broker, error) {
	if err := initBroker(); err != nil {
		return nil, err
	}
	b := &broker{boxes: map[int]uint32{}, services: map[int]uint32{}}
	b.cond = sync.NewCond(&b.mu)
	var kr int32
	task, right, name := machTask, uint32(machPortRightReceive), uint32(0)
	namep := unsafe.Pointer(&name)
	mpAllocate.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&right), unsafe.Pointer(&namep))
	if kr != 0 {
		return nil, errors.New("mach_port_allocate failed")
	}
	disp := uint32(machMsgTypeMakeSend)
	mpInsertRight.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&name), unsafe.Pointer(&name), unsafe.Pointer(&disp))
	if kr != 0 {
		return nil, errors.New("mach_port_insert_right failed")
	}
	b.port = name
	go b.serve()
	return b, nil
}

// serve takes registrations, and stubs' shared-code requests
// (codereg_darwin.go), until the port is destroyed (close).
func (b *broker) serve() {
	for {
		var r [64]uint32 // a message and its audit trailer
		rp := unsafe.Pointer(&r)
		opt, size, port, zero := int32(machRcvMsg|machRcvTrailerAudit), uint32(0), b.port, uint32(0)
		rcvSize := uint32(unsafe.Sizeof(r))
		var kr int32
		mMsg.call(unsafe.Pointer(&kr), unsafe.Pointer(&rp), unsafe.Pointer(&opt), unsafe.Pointer(&size), unsafe.Pointer(&rcvSize), unsafe.Pointer(&port), unsafe.Pointer(&zero), unsafe.Pointer(&zero))
		if kr != 0 {
			if kr == machRcvTooLarge {
				continue // a malformed message, dropped by the kernel
			}
			b.mu.Lock()
			b.dead = true
			b.cond.Broadcast()
			b.mu.Unlock()
			return
		}
		h := (*machMsgHeader)(rp)
		pid := int(r[(h.size+3)/4+5+5]) // the trailer's audit token: its pid is word 5
		complex := h.bits&machMsgBitsComplex != 0
		switch h.id {
		case brokerHot, brokerCool:
			if complex || h.size < 40 {
				b.drop(h.remote)
				continue
			}
			go b.codeReq(h.id, pid, objPage{obj: uint64(r[6]), off: uint64(r[8]) | uint64(r[9])<<32}, h.remote)
		case brokerRegister, brokerService:
			if !complex || r[6] != 1 || r[7] == 0 {
				b.drop(h.remote)
				continue
			}
			set := b.boxes
			if h.id == brokerService {
				set = b.services
			}
			b.mu.Lock()
			old := set[pid] // a stub that has ended, its pid now another's
			set[pid] = r[7]
			b.cond.Broadcast()
			b.mu.Unlock()
			b.drop(old)
			if h.id == brokerService {
				// What is held as it joins; a HOT after it reaches it.
				b.replyHotSet(h.remote, b.code.join(pid, brokerLink{b, pid}))
			}
		default:
			if complex {
				b.drop(r[7])
			}
			b.drop(h.remote)
		}
	}
}

func (b *broker) drop(name uint32) {
	if name == 0 {
		return
	}
	var kr int32
	task := machTask
	mpDeallocate.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&name))
}

// box is stub pid's mailbox, once it has registered; 0 if it ended first
// (ended reports that). A stub registers before it starts serving, so a
// ready stub's registration is on its way.
func (b *broker) box(pid int, ended func() bool) uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if name, ok := b.boxes[pid]; ok {
			return name
		}
		if b.dead || ended() {
			return 0
		}
		b.cond.Wait()
	}
}

// ended forgets stub pid's ports.
func (b *broker) ended(pid int) {
	b.mu.Lock()
	box, svc := b.boxes[pid], b.services[pid]
	delete(b.boxes, pid)
	delete(b.services, pid)
	b.cond.Broadcast()
	b.mu.Unlock()
	b.drop(box)
	b.drop(svc)
	b.code.leave(pid)
}

// service is stub pid's service port, once it has registered; 0 if it ended
// first.
func (b *broker) service(pid int, ended func() bool) uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if name, ok := b.services[pid]; ok {
			return name
		}
		if b.dead || ended() {
			return 0
		}
		b.cond.Wait()
	}
}

// mapsOf asks stub process pid for its guest's mappings, which its service
// thread answers whatever the guest's threads are doing; false if the stub
// ended first. The request carries a send-once right for the answer: if the
// stub ends holding it, the kernel answers in its place.
func (b *broker) mapsOf(pid int, ended func() bool) ([]mapEnt, bool) {
	buf, ok := b.ask(pid, ended, brokerMaps, brokerMapsReply, 40)
	if !ok {
		return nil, false
	}
	out := make([]mapEnt, len(buf)/40)
	for i := range out {
		e := buf[i*40:]
		out[i] = mapEnt{le64(e), le64(e[8:]), le64(e[16:]), le64(e[24:]), le64(e[32:])}
	}
	return out, true
}

// smapsOf is mapsOf with each mapping's counts, as smapsRecords reads them.
func (b *broker) smapsOf(pid int, ended func() bool) ([]byte, bool) {
	return b.ask(pid, ended, brokerSmaps, brokerSmapsReply, smapEntSize)
}

// ask sends stub pid's service request id and returns the records of rec
// bytes its reply (replyID) carries.
func (b *broker) ask(pid int, ended func() bool, id, replyID int32, rec uint64) ([]byte, bool) {
	svc := b.service(pid, ended)
	if svc == 0 {
		return nil, false
	}
	var kr int32
	task, right, reply := machTask, uint32(machPortRightReceive), uint32(0)
	rp := unsafe.Pointer(&reply)
	mpAllocate.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&right), unsafe.Pointer(&rp))
	if kr != 0 {
		return nil, false
	}
	defer func() {
		delta := int32(-1)
		mpModRefs.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&reply), unsafe.Pointer(&right), unsafe.Pointer(&delta))
	}()
	req := machMsgHeader{bits: machMsgTypeCopySend | machMsgTypeMakeSendOnce<<8, size: uint32(unsafe.Sizeof(machMsgHeader{})), remote: svc, local: reply, id: id}
	mp := unsafe.Pointer(&req)
	opt, size, zero := int32(machSendMsg), req.size, uint32(0)
	mMsg.call(unsafe.Pointer(&kr), unsafe.Pointer(&mp), unsafe.Pointer(&opt), unsafe.Pointer(&size), unsafe.Pointer(&zero), unsafe.Pointer(&zero), unsafe.Pointer(&zero), unsafe.Pointer(&zero))
	if kr != 0 {
		return nil, false
	}
	// The answer: header, descriptor count, the out-of-line descriptor (its
	// address at byte 28, its size at 40) and the entry count (44).
	var in [32]uint32
	ip := unsafe.Pointer(&in)
	opt, rcvSize := int32(machRcvMsg), uint32(unsafe.Sizeof(in))
	mMsg.call(unsafe.Pointer(&kr), unsafe.Pointer(&ip), unsafe.Pointer(&opt), unsafe.Pointer(&zero), unsafe.Pointer(&rcvSize), unsafe.Pointer(&reply), unsafe.Pointer(&zero), unsafe.Pointer(&zero))
	if kr != 0 || int32(in[5]) != replyID || in[0]&machMsgBitsComplex == 0 || in[6] != 1 {
		return nil, false // the kernel's send-once notification: the stub ended
	}
	addr, n, count := uint64(in[7])|uint64(in[8])<<32, uint64(in[10]), uint64(in[11])
	if n == 0 {
		return nil, true
	}
	defer func() {
		var r int32
		mVmDeallocate.call(unsafe.Pointer(&r), unsafe.Pointer(&task), unsafe.Pointer(&addr), unsafe.Pointer(&n))
	}()
	if count*rec > n {
		return nil, false
	}
	buf := make([]byte, count*rec)
	if count == 0 {
		return nil, true
	}
	dst := unsafe.Pointer(&buf[0])
	nb := uint64(len(buf))
	var ret unsafe.Pointer
	cMemcpy.call(unsafe.Pointer(&ret), unsafe.Pointer(&dst), unsafe.Pointer(&addr), unsafe.Pointer(&nb))
	return buf, true
}

// sendChild gives stub pid a send right to its fork child's mailbox, with
// the child's pid, or no port and pid 0 if the child has ended: the parent
// waits for one or the other. false if the parent is gone.
func (b *broker) sendChild(pid, child int, ended func(int) bool) bool {
	dst := b.box(pid, func() bool { return ended(pid) })
	if dst == 0 {
		return false
	}
	box := b.box(child, func() bool { return ended(child) })
	if box == 0 {
		child = 0
	}
	m := brokerMsg{
		h:    machMsgHeader{bits: machMsgTypeCopySend | machMsgBitsComplex, size: uint32(unsafe.Sizeof(brokerMsg{})), remote: dst, id: brokerChild},
		n:    1,
		port: machPortDescriptor{name: box, disposition: machMsgTypeCopySend, typ: machMsgPortDesc},
		pid:  int32(child),
	}
	mp := unsafe.Pointer(&m)
	opt, size, zero := int32(machSendMsg), m.h.size, uint32(0)
	var kr int32
	mMsg.call(unsafe.Pointer(&kr), unsafe.Pointer(&mp), unsafe.Pointer(&opt), unsafe.Pointer(&size), unsafe.Pointer(&zero), unsafe.Pointer(&zero), unsafe.Pointer(&zero), unsafe.Pointer(&zero))
	return kr == 0
}

// close destroys the broker's port, which ends serve.
func (b *broker) close() {
	var kr int32
	task, name, right, delta := machTask, b.port, uint32(machPortRightReceive), int32(-1)
	mpModRefs.call(unsafe.Pointer(&kr), unsafe.Pointer(&task), unsafe.Pointer(&name), unsafe.Pointer(&right), unsafe.Pointer(&delta))
	b.mu.Lock()
	for _, n := range b.boxes {
		b.drop(n)
	}
	for _, n := range b.services {
		b.drop(n)
	}
	b.boxes = map[int]uint32{}
	b.services = map[int]uint32{}
	b.mu.Unlock()
}
