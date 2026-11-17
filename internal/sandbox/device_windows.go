//go:build windows

package sandbox

// ensureDevices is a no-op on Windows: host-backed serial Devices are a
// follow-up. The API (Config.Devices) exists on every platform.
func (g *dguest) ensureDevices() {}

// openDeviceBacking reports that rdev is not an attached Device on Windows.
func (g *dguest) openDeviceBacking(gp string, rdev uint64, flags int64) (int64, bool) {
	return 0, false
}
