//go:build darwin

package sandbox

// An arm64 instruction never runs from one page into the next.
func noInsnEnd(uint64) uint64 { return 0 }

func init() {
	regSys(lcAnonSites, func(g *dguest, a [6]uint64) int64 {
		return g.mp.anon.sites(g, uint64(g.guestPageSize()), a[0], a[1], g.getBuf, noInsnEnd)
	})
	regSys(lcAnonLearn, func(g *dguest, a [6]uint64) int64 {
		return g.mp.anon.learn(g, uint64(g.guestPageSize()), int(a[0]), g.getBuf, noInsnEnd)
	})
}
