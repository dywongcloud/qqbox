//go:build darwin || linux || windows

package sandbox

import (
	"os"
	"sync"
)

// DeviceKind is how a Device is backed on the host.
type DeviceKind int

const (
	// DeviceSerial is a byte stream carried over a host socketpair: the guest
	// reads and writes the device node under /dev, the host holds the other end
	// (Device.Host), giving a direct channel with no network.
	DeviceSerial DeviceKind = iota
)

// Device attaches a character device to the guest at Path (under /dev), backed
// on the host for a direct host-guest channel with no network. While the run is
// live, Host is the supervisor's end of the channel; it is nil before the run
// starts and after it ends. Pass a *Device in Config.Devices; a Device serves
// one run at a time.
type Device struct {
	Path string
	Kind DeviceKind

	mu    sync.Mutex
	host  *os.File
	ready chan struct{}
}

// Host is the supervisor's end of the device's channel while the run is live, or
// nil before it starts and after it ends.
func (d *Device) Host() *os.File {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.host
}

// Ready is closed once the run has started and Host is available, so a caller
// that drives the device from its own goroutine can wait on it instead of
// polling Host.
func (d *Device) Ready() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readyCh()
}

func (d *Device) publish(f *os.File) {
	d.mu.Lock()
	d.host = f
	close(d.readyCh())
	d.mu.Unlock()
}

// readyCh returns the readiness channel, creating it on first use; the caller
// holds mu.
func (d *Device) readyCh() chan struct{} {
	if d.ready == nil {
		d.ready = make(chan struct{})
	}
	return d.ready
}

func (d *Device) revoke() {
	d.mu.Lock()
	if d.host != nil {
		d.host.Close()
		d.host = nil
	}
	d.mu.Unlock()
}

// lbDevMajor is the character-device major qqbox gives an attached Device; the
// minor indexes Config.Devices. hostDevByRdev does not claim this major, so
// openVirtualDev routes it to the device registry instead of a host device.
const lbDevMajor = 0xada

// devSet is a run's attached Devices: the guest-side end of each backing keyed
// by the reserved device number its /dev node carries (for openVirtualDev), plus
// the Devices themselves (to revoke at teardown).
type devSet struct {
	once sync.Once
	mu   sync.Mutex
	back map[uint64]*os.File
	devs []*Device
}

// closeDevices tears down the run's device backings and revokes their host ends.
func (fs *dfs) closeDevices() {
	fs.devs.mu.Lock()
	for _, gf := range fs.devs.back {
		gf.Close()
	}
	fs.devs.back = nil
	devs := fs.devs.devs
	fs.devs.devs = nil
	fs.devs.mu.Unlock()
	for _, d := range devs {
		d.revoke()
	}
}
