#include "textflag.h"

// func a64sysregNextHit(p *byte, n, from int) int
// Scans p[from:n] for a word w with (w & 0xffffffe0) in
// {0xd53bd040 mrs tpidr, 0xd51bd040 msr tpidr, 0xd53b0020 mrs ctr}, four words
// (16 bytes) per NEON step; the <16-byte tail is checked word by word.
// Returns the block base (or tail word offset) of the first hit, else n.
TEXT ·a64sysregNextHit(SB), NOSPLIT, $0-32
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	MOVD from+16(FP), R2

	MOVD $0xffffffe0, R3 // the sysreg field mask
	MOVD $0xd53bd040, R6 // mrs tpidr_el0
	MOVD $0xd51bd040, R7 // msr tpidr_el0
	MOVD $0xd53b0020, R8 // mrs ctr_el0
	VDUP R3, V16.S4
	VDUP R6, V17.S4
	VDUP R7, V18.S4
	VDUP R8, V19.S4

loop:
	ADD  $16, R2, R11
	CMP  R1, R11       // R11 - R1
	BGT  tail          // fewer than 16 bytes left
	ADD  R0, R2, R12
	VLD1 (R12), [V0.B16]
	VAND V16.B16, V0.B16, V1.B16
	VCMEQ V17.S4, V1.S4, V2.S4
	VCMEQ V18.S4, V1.S4, V3.S4
	VCMEQ V19.S4, V1.S4, V4.S4
	VORR V3.B16, V2.B16, V2.B16
	VORR V4.B16, V2.B16, V2.B16
	VADDV V2.S4, V5
	VMOV V5.S[0], R4
	CBNZ R4, done      // some lane matched: R2 is the block base
	ADD  $16, R2, R2
	B    loop

tail:
	ADD $4, R2, R10
	CMP R1, R10        // R10 - R1
	BGT none           // no full word left
	ADD R0, R2, R13
	MOVWU (R13), R5
	AND R3, R5, R5
	CMP R6, R5
	BEQ done
	CMP R7, R5
	BEQ done
	CMP R8, R5
	BEQ done
	ADD $4, R2, R2
	B   tail

done:
	MOVD R2, ret+24(FP)
	RET
none:
	MOVD R1, ret+24(FP)
	RET
