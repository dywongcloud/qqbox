// seccomp_trap.c — a SECCOMP_RET_TRAP must raise SIGSYS with a siginfo naming
// the trapped syscall, as a SIGSYS handler (Chromium installs one) relies on.
#define _GNU_SOURCE
#include <errno.h>
#include <signal.h>
#include <stddef.h>
#include <stdio.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <unistd.h>

#ifndef SYS_SECCOMP
#define SYS_SECCOMP 1
#endif

static volatile sig_atomic_t fired, gotCode, gotSys, gotArch;

static void handler(int sig, siginfo_t *si, void *uc) {
	fired = 1;
	gotCode = si->si_code;
	gotSys = si->si_syscall;
	gotArch = si->si_arch;
}

int main(void) {
#if defined(__x86_64__)
	const unsigned arch = AUDIT_ARCH_X86_64;
#elif defined(__aarch64__)
	const unsigned arch = AUDIT_ARCH_AARCH64;
#else
	return 0;
#endif
	struct sigaction sa = {0};
	sa.sa_sigaction = handler;
	sa.sa_flags = SA_SIGINFO;
	sigemptyset(&sa.sa_mask);
	if (sigaction(SIGSYS, &sa, 0) != 0) {
		perror("sigaction");
		return 10;
	}
	struct sock_filter f[] = {
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_getuid, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_TRAP),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
	};
	struct sock_fprog prog = {.len = sizeof f / sizeof f[0], .filter = f};
	if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) return 11;
	if (syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, 0, &prog) != 0) {
		perror("seccomp");
		return 12;
	}
	syscall(__NR_getuid); // trapped -> SIGSYS
	if (!fired) {
		fprintf(stderr, "SIGSYS not delivered\n");
		return 13;
	}
	if (gotCode != SYS_SECCOMP) {
		fprintf(stderr, "si_code=%d want %d\n", (int)gotCode, SYS_SECCOMP);
		return 14;
	}
	if (gotSys != __NR_getuid) {
		fprintf(stderr, "si_syscall=%d want %d\n", (int)gotSys, (int)__NR_getuid);
		return 15;
	}
	if ((unsigned)gotArch != arch) {
		fprintf(stderr, "si_arch=%u want %u\n", (unsigned)gotArch, arch);
		return 16;
	}
	return 0;
}
