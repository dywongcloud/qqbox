// Forks, says so, then both processes compute forever without a syscall.
#include <stdio.h>
#include <unistd.h>

int main(void) {
	if (fork() > 0) {
		puts("forked");
		fflush(stdout);
	}
	for (volatile unsigned long n = 0;; n++) {
	}
}
