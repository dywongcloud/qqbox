// seccomp_sigreturn.c — a guest whose own seccomp filter TRAPs rt_sigreturn.
// Returning from the SIGUSR1 handler executes rt_sigreturn, which the filter
// must trap -> SIGSYS -> the SIGSYS handler exits 42. If the sandbox completes
// rt_sigreturn without consulting the guest's filter, control instead reaches
// exit(1). The filter must apply to rt_sigreturn however the sandbox services
// it (trampoline, trap, or the seccomp backstop), and at any nesting depth.
#define _GNU_SOURCE
#include <signal.h>
#include <stddef.h>
#include <unistd.h>
#include <linux/filter.h>
#include <linux/seccomp.h>
#include <sys/prctl.h>
#include <sys/syscall.h>

#if defined(__x86_64__)
#define NR_RT_SIGRETURN 15
#elif defined(__aarch64__)
#define NR_RT_SIGRETURN 139
#else
#define NR_RT_SIGRETURN 0
#endif

static void sigsys(int s, siginfo_t *si, void *u) { _exit(42); }
static void sigusr1(int s) { /* return -> rt_sigreturn */ }

int main(void) {
	if (NR_RT_SIGRETURN == 0) return 0;
	struct sigaction a = {0};
	a.sa_sigaction = sigsys;
	a.sa_flags = SA_SIGINFO;
	sigemptyset(&a.sa_mask);
	sigaction(SIGSYS, &a, 0);
	struct sigaction u = {0};
	u.sa_handler = sigusr1;
	sigemptyset(&u.sa_mask);
	sigaction(SIGUSR1, &u, 0);
	struct sock_filter f[] = {
		BPF_STMT(BPF_LD | BPF_W | BPF_ABS, offsetof(struct seccomp_data, nr)),
		BPF_JUMP(BPF_JMP | BPF_JEQ | BPF_K, NR_RT_SIGRETURN, 0, 1),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_TRAP),
		BPF_STMT(BPF_RET | BPF_K, SECCOMP_RET_ALLOW),
	};
	struct sock_fprog prog = {.len = sizeof f / sizeof f[0], .filter = f};
	if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) return 11;
	if (syscall(__NR_seccomp, SECCOMP_SET_MODE_FILTER, 0, &prog) != 0) return 12;
	raise(SIGUSR1); // handler returns -> rt_sigreturn -> SIGSYS -> exit 42
	return 1;       // reached only if rt_sigreturn was not trapped
}
