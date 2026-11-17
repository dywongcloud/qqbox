#include "textflag.h"

// func cputicks() uint64
// Reads the virtual count register (cntvct_el0), the arm64 system counter:
// a steady, process-wide tick clock, which is all the spin controller needs.
TEXT ·cputicks(SB),NOSPLIT,$0-8
	WORD	$0xd53be040 // mrs x0, cntvct_el0
	MOVD	R0, ret+0(FP)
	RET

// func spinHint()
// YIELD: a spin-wait hint, yielding the core to the SMT sibling and lowering
// spin power.
TEXT ·spinHint(SB),NOSPLIT,$0-0
	YIELD
	RET
