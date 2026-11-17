#include "textflag.h"

// func cpuidWaitpkg() bool
// CPUID leaf 7, subleaf 0: WAITPKG (UMONITOR/UMWAIT/TPAUSE) is ECX bit 5.
TEXT ·cpuidWaitpkg(SB),NOSPLIT,$0-1
	MOVL	$7, AX
	MOVL	$0, CX
	CPUID
	SHRL	$5, CX
	ANDL	$1, CX
	MOVB	CX, ret+0(FP)
	RET

// func umonitor(addr unsafe.Pointer)
// UMONITOR RAX (F3 0F AE F0): arm the address monitor on the cache line at RAX.
TEXT ·umonitor(SB),NOSPLIT,$0-8
	MOVQ	addr+0(FP), AX
	BYTE	$0xF3; BYTE $0x0F; BYTE $0xAE; BYTE $0xF0
	RET

// func umwait(ctrl uint32, deadline uint64)
// UMWAIT ECX (F2 0F AE F1): wait in a low-power state until a store lands on the
// armed line, the TSC reaches EDX:EAX, or an event occurs. ECX bit 0 is the
// C-state hint (1 = C0.1, lighter and faster to wake).
TEXT ·umwait(SB),NOSPLIT,$0-16
	MOVL	ctrl+0(FP), CX
	MOVQ	deadline+8(FP), AX
	MOVQ	AX, DX
	SHRQ	$32, DX
	BYTE	$0xF2; BYTE $0x0F; BYTE $0xAE; BYTE $0xF1
	RET
