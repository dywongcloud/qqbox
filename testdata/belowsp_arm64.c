// Checks that a syscall leaves memory below sp alone, as Linux does (Go's
// runtime.clone relies on it), then execs itself once so the second image
// starts from whichever path served the first execve.
#include <stdio.h>
#include <unistd.h>

unsigned long belowsp(void);

__asm__(".globl belowsp\n"
	".type belowsp, %function\n"
	"belowsp:\n"
	"  mov x9, #0x111\n stur x9, [sp, #-8]\n"
	"  mov x9, #0x222\n stur x9, [sp, #-16]\n"
	"  mov x9, #0x333\n stur x9, [sp, #-24]\n"
	"  mov x9, #0x444\n stur x9, [sp, #-64]\n"
	"  mov x9, #0x555\n stur x9, [sp, #-128]\n"
	"  mov x8, #172\n svc #0\n"
	"  mov x0, #0\n"
	"  ldur x9, [sp, #-8]\n cmp x9, #0x111\n cinc x0, x0, ne\n"
	"  ldur x9, [sp, #-16]\n cmp x9, #0x222\n cinc x0, x0, ne\n"
	"  ldur x9, [sp, #-24]\n cmp x9, #0x333\n cinc x0, x0, ne\n"
	"  ldur x9, [sp, #-64]\n cmp x9, #0x444\n cinc x0, x0, ne\n"
	"  ldur x9, [sp, #-128]\n cmp x9, #0x555\n cinc x0, x0, ne\n"
	"  ret\n");

int main(int argc, char **argv)
{
	unsigned long bad = belowsp();
	printf("%s: below sp %s\n", argc > 1 ? "second" : "first", bad ? "clobbered" : "preserved");
	fflush(stdout);
	if (argc > 1)
		return bad != 0;
	char *args[] = { argv[0], "again", 0 };
	execv(argv[0], args);
	perror("execv");
	return 2;
}
