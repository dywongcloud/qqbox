// seccomp.c — checks that laboon enforces an installed seccomp-bpf filter in
// the guest: a filter that fails getuid with EPERM but allows everything else
// must make getuid() return EPERM while getpid() still works.
#define _GNU_SOURCE
#include <errno.h>
#include <stddef.h>
#include <stdio.h>
#include <linux/audit.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/wait.h>
#include <unistd.h>

int main(void) {
#if defined(__x86_64__)
	const unsigned arch = AUDIT_ARCH_X86_64;
#elif defined(__aarch64__)
	const unsigned arch = AUDIT_ARCH_AARCH64;
#else
	return 0; // unsupported arch: skip
#endif
	struct sock_filter f[] = {
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, arch)),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, arch, 0, 4),
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, __NR_getuid, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ERRNO | (EPERM & SECCOMP_RET_DATA)),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_KILL), // foreign arch
	};
	struct sock_fprog prog = {.len = sizeof f / sizeof f[0], .filter = f};

	if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) {
		perror("PR_SET_NO_NEW_PRIVS");
		return 10;
	}
	if (syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, 0, &prog) != 0) {
		perror("seccomp(SET_MODE_FILTER)");
		return 11;
	}
	// getuid must now be blocked with EPERM.
	errno = 0;
	long uid = syscall(__NR_getuid);
	if (uid != -1 || errno != EPERM) {
		fprintf(stderr, "getuid=%ld errno=%d, want -1/EPERM\n", uid, errno);
		return 12;
	}
	// getpid must still work.
	if (syscall(__NR_getpid) <= 0) {
		perror("getpid");
		return 13;
	}
	// A fork child inherits the filter.
	pid_t kid = fork();
	if (kid == 0) {
		errno = 0;
		long u = syscall(__NR_getuid);
		_exit(u == -1 && errno == EPERM ? 0 : 1);
	}
	int st = 0;
	waitpid(kid, &st, 0);
	if (!WIFEXITED(st) || WEXITSTATUS(st) != 0) {
		fprintf(stderr, "child did not inherit the filter (status %d)\n", st);
		return 14;
	}
	return 0;
}
