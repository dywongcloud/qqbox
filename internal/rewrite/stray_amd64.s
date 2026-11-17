#include "textflag.h"

DATA b0f<>+0(SB)/8, $0x0f0f0f0f0f0f0f0f
DATA b0f<>+8(SB)/8, $0x0f0f0f0f0f0f0f0f
GLOBL b0f<>(SB), RODATA|NOPTR, $16
DATA b05<>+0(SB)/8, $0x0505050505050505
DATA b05<>+8(SB)/8, $0x0505050505050505
GLOBL b05<>(SB), RODATA|NOPTR, $16

// func strayNextHit(p *byte, n, from int) int
// Finds the first `0f 05` pair in p[from:n] (SSE2, 16 bytes per step; the
// overlapping load at off+1 catches a pair that straddles a 16-byte block).
TEXT ·strayNextHit(SB), NOSPLIT, $0-32
	MOVQ p+0(FP), AX
	MOVQ n+8(FP), BX
	MOVQ from+16(FP), CX
	MOVOU b0f<>(SB), X0
	MOVOU b05<>(SB), X1
loop:
	MOVQ CX, DX
	ADDQ $17, DX       // need [off, off+17) in range for both loads
	CMPQ DX, BX
	JGT  tail
	LEAQ (AX)(CX*1), SI
	MOVOU (SI), X2     // bytes [off, off+16)
	MOVOU 1(SI), X3    // bytes [off+1, off+17)
	PCMPEQB X0, X2     // lane == 0x0f
	PCMPEQB X1, X3     // next lane == 0x05
	PAND X3, X2
	PMOVMSKB X2, DI
	TESTL DI, DI
	JNZ  hit
	ADDQ $16, CX
	JMP  loop
hit:
	BSFL DI, DI
	ADDQ DI, CX
	MOVQ CX, ret+24(FP)
	RET
tail:
	MOVQ CX, DX
	ADDQ $1, DX
	CMPQ DX, BX
	JGE  none          // no room for a pair
	LEAQ (AX)(CX*1), SI
	MOVBLZX (SI), DI
	CMPL DI, $0x0f
	JNE  tnext
	MOVBLZX 1(SI), DI
	CMPL DI, $0x05
	JEQ  found
tnext:
	ADDQ $1, CX
	JMP  tail
found:
	MOVQ CX, ret+24(FP)
	RET
none:
	MOVQ BX, ret+24(FP)
	RET
