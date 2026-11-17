#include "textflag.h"

// func cputicks() uint64
// Reads the time-stamp counter. On the invariant-TSC machines this targets it
// is a steady, process-wide cycle clock, which is all the spin controller needs.
TEXT ·cputicks(SB),NOSPLIT,$0-8
	RDTSC
	SHLQ	$32, DX
	ORQ	DX, AX
	MOVQ	AX, ret+0(FP)
	RET

// func spinHint()
// PAUSE: a spin-wait hint. It yields the physical core's pipeline to the SMT
// sibling for the duration (so a busy-waiter does not starve a sibling doing
// real work) and lowers spin power, at the cost of a slightly coarser poll.
TEXT ·spinHint(SB),NOSPLIT,$0-0
	PAUSE
	RET
