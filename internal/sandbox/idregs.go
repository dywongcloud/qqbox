//go:build darwin || windows

package sandbox

// The AArch64 feature ID registers the guest reads with mrs, built from the
// host features the run already presents in AT_HWCAP/HWCAP2. On Linux the
// kernel emulates these EL0 reads; on native macOS and Windows arm64 they
// trap, and the stub serves them from here. Only the ISAR registers are
// built: their every field is a feature level, so a field left 0 for a
// feature the host lacks (or one we do not detect) reads as "absent", never
// claiming what the host cannot run. The registers that also hold layout
// values (ID_AA64MMFR*, ID_AA64PFR0's exception-level and address fields)
// are not built here; a wrong value there is unsafe, so those reads keep
// trapping.
//
// The HWCAP bit numbers are Linux's (hostinfo_*.go map the host's features to
// them); the ID-register field encodings are the architecture's.

const (
	hwFP       = 1 << 0
	hwASIMD    = 1 << 1
	hwAES      = 1 << 3
	hwPMULL    = 1 << 4
	hwSHA1     = 1 << 5
	hwSHA2     = 1 << 6
	hwCRC32    = 1 << 7
	hwATOMICS  = 1 << 8
	hwASIMDRDM = 1 << 12
	hwJSCVT    = 1 << 13
	hwFCMA     = 1 << 14
	hwLRCPC    = 1 << 15
	hwDCPOP    = 1 << 16
	hwSHA3     = 1 << 17
	hwASIMDDP  = 1 << 20
	hwSHA512   = 1 << 21
	hwASIMDFHM = 1 << 23
	hwLRCPC2   = 1 << 26 // ilrcpc
	hwFLAGM    = 1 << 27
	hwSB       = 1 << 29

	hw2DCPODP = 1 << 0  // in HWCAP2
	hw2FLAGM2 = 1 << 7  // in HWCAP2
	hw2FRINT  = 1 << 8  // in HWCAP2
	hw2I8MM   = 1 << 13 // in HWCAP2
	hw2BF16   = 1 << 14 // in HWCAP2
	hw2RPRES  = 1 << 21 // in HWCAP2
	hw2CSSC   = 1 << 34 // in HWCAP2
)

// field puts level v in the 4-bit field at bit lo.
func field(lo uint, v uint64) uint64 { return v << lo }

// isarFromHwcap builds ID_AA64ISAR0/1/2_EL1 from the host HWCAP/HWCAP2.
func isarFromHwcap(hwcap, hwcap2 uint64) (isar0, isar1, isar2 uint64) {
	has := func(h uint64) bool { return hwcap&h != 0 }
	has2 := func(h uint64) bool { return hwcap2&h != 0 }

	if has(hwPMULL) {
		isar0 |= field(4, 2) // AES: PMULL implies AES
	} else if has(hwAES) {
		isar0 |= field(4, 1)
	}
	if has(hwSHA1) {
		isar0 |= field(8, 1)
	}
	if has(hwSHA512) {
		isar0 |= field(12, 2)
	} else if has(hwSHA2) {
		isar0 |= field(12, 1)
	}
	if has(hwCRC32) {
		isar0 |= field(16, 1)
	}
	if has(hwATOMICS) {
		isar0 |= field(20, 2) // FEAT_LSE
	}
	if has(hwASIMDRDM) {
		isar0 |= field(28, 1)
	}
	if has(hwSHA3) {
		isar0 |= field(32, 1)
	}
	if has(hwASIMDDP) {
		isar0 |= field(44, 1)
	}
	if has(hwASIMDFHM) {
		isar0 |= field(48, 1)
	}
	if has2(hw2FLAGM2) {
		isar0 |= field(52, 2)
	} else if has(hwFLAGM) {
		isar0 |= field(52, 1)
	}

	if has2(hw2DCPODP) {
		isar1 |= field(0, 2)
	} else if has(hwDCPOP) {
		isar1 |= field(0, 1)
	}
	if has(hwJSCVT) {
		isar1 |= field(12, 1)
	}
	if has(hwFCMA) {
		isar1 |= field(16, 1)
	}
	if has(hwLRCPC2) {
		isar1 |= field(20, 2)
	} else if has(hwLRCPC) {
		isar1 |= field(20, 1)
	}
	if has2(hw2FRINT) {
		isar1 |= field(32, 1)
	}
	if has(hwSB) {
		isar1 |= field(36, 1)
	}
	if has2(hw2BF16) {
		isar1 |= field(44, 1)
	}
	if has2(hw2I8MM) {
		isar1 |= field(52, 1)
	}

	if has2(hw2RPRES) {
		isar2 |= field(4, 1)
	}
	if has2(hw2CSSC) {
		isar2 |= field(52, 1)
	}
	return isar0, isar1, isar2
}
