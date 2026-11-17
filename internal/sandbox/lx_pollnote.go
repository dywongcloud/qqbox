//go:build darwin || linux || windows

package sandbox

// pollNote is protected by its owner's lock. Windows attaches the run bus;
// Unix continues watching the owner's readiness pipe.
type pollNote struct {
	stamp  uint64
	notify func()
}

func (p *pollNote) touch() {
	p.stamp++
	if p.notify != nil {
		p.notify()
	}
}
