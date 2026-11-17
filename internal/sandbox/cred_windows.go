package sandbox

// AF_UNIX credential passing is handled within the Windows backend's own
// in-supervisor socket model (net_windows.go); the shared receive path adds no
// SCM_CREDENTIALS here.

func (g *dguest) recvCreds(d *dfile, room int) ([]byte, uint32) { return nil, 0 }
