// Checks that a rewritten svc preserves every register but x0, as the arm64
// Linux ABI does: x1-x30 (x8 keeps the syscall number), NZCV and d0-d7.
#include <stdio.h>
#include <string.h>

unsigned long check(unsigned long *out, unsigned long nr);

__asm__(".globl check\n"
	".type check, %function\n"
	"check:\n"
	"  stp x29, x30, [sp, #-16]!\n"
	"  stp x27, x28, [sp, #-16]!\n"
	"  stp x25, x26, [sp, #-16]!\n"
	"  stp x23, x24, [sp, #-16]!\n"
	"  stp x21, x22, [sp, #-16]!\n"
	"  stp x19, x20, [sp, #-16]!\n"
	"  stp x0, x1, [sp, #-16]!\n"
	"  mov x8, x1\n"
	"  mov x0, #0\n"
	"  mov x1, #0x101\n mov x2, #0x202\n mov x3, #0x303\n mov x4, #0x404\n"
	"  mov x5, #0x505\n mov x6, #0x606\n mov x7, #0x707\n mov x9, #0x909\n"
	"  mov x10, #0xa0a\n mov x11, #0xb0b\n mov x12, #0xc0c\n mov x13, #0xd0d\n"
	"  mov x14, #0xe0e\n mov x15, #0xf0f\n mov x16, #0x1010\n mov x17, #0x1111\n"
	"  mov x18, #0x1212\n mov x19, #0x1313\n mov x20, #0x1414\n mov x21, #0x1515\n"
	"  mov x22, #0x1616\n mov x23, #0x1717\n mov x24, #0x1818\n mov x25, #0x1919\n"
	"  mov x26, #0x1a1a\n mov x27, #0x1b1b\n mov x28, #0x1c1c\n mov x29, #0x1d1d\n"
	"  mov x30, #0x1e1e\n"
	"  fmov d0, x1\n fmov d1, x2\n fmov d2, x3\n fmov d3, x4\n"
	"  fmov d4, x5\n fmov d5, x6\n fmov d6, x7\n fmov d7, x9\n"
	"  mov x0, #0xa0000000\n msr nzcv, x0\n mov x0, #0\n"
	"  svc #0\n"
	"  sub sp, sp, #320\n"
	"  stp x0, x1, [sp, #0]\n stp x2, x3, [sp, #16]\n stp x4, x5, [sp, #32]\n"
	"  stp x6, x7, [sp, #48]\n stp x8, x9, [sp, #64]\n stp x10, x11, [sp, #80]\n"
	"  stp x12, x13, [sp, #96]\n stp x14, x15, [sp, #112]\n stp x16, x17, [sp, #128]\n"
	"  stp x18, x19, [sp, #144]\n stp x20, x21, [sp, #160]\n stp x22, x23, [sp, #176]\n"
	"  stp x24, x25, [sp, #192]\n stp x26, x27, [sp, #208]\n stp x28, x29, [sp, #224]\n"
	"  str x30, [sp, #240]\n mrs x9, nzcv\n str x9, [sp, #248]\n"
	"  stp d0, d1, [sp, #256]\n stp d2, d3, [sp, #272]\n stp d4, d5, [sp, #288]\n"
	"  stp d6, d7, [sp, #304]\n"
	"  ldr x0, [sp, #320]\n"
	"  mov x1, #0\n"
	"1: ldr x2, [sp, x1, lsl #3]\n str x2, [x0, x1, lsl #3]\n"
	"  add x1, x1, #1\n cmp x1, #40\n b.ne 1b\n"
	"  add sp, sp, #336\n"
	"  ldp x19, x20, [sp], #16\n"
	"  ldp x21, x22, [sp], #16\n"
	"  ldp x23, x24, [sp], #16\n"
	"  ldp x25, x26, [sp], #16\n"
	"  ldp x27, x28, [sp], #16\n"
	"  ldp x29, x30, [sp], #16\n"
	"  ret\n");

static int verify(const char *what, unsigned long nr)
{
	unsigned long r[40];
	memset(r, 0, sizeof r);
	check(r, nr);
	int bad = 0;
	for (int i = 1; i <= 30; i++) {
		unsigned long want = i == 8 ? nr : (unsigned long)i * 0x101;
		if (r[i] != want) {
			printf("%s: x%d = %#lx, want %#lx\n", what, i, r[i], want);
			bad = 1;
		}
	}
	if (r[31] != 0xa0000000) {
		printf("%s: nzcv = %#lx\n", what, r[31]);
		bad = 1;
	}
	static const int d[8] = { 1, 2, 3, 4, 5, 6, 7, 9 };
	for (int i = 0; i < 8; i++) {
		if (r[32 + i] != (unsigned long)d[i] * 0x101) {
			printf("%s: d%d = %#lx\n", what, i, r[32 + i]);
			bad = 1;
		}
	}
	return bad;
}

int main(void)
{
	int bad = verify("getpid", 172) | verify("gettid", 178) | verify("sched_yield", 124);
	puts(bad ? "regs clobbered" : "regs preserved");
	return bad;
}
