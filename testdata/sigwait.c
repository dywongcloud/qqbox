// sigwait.c: install a SIGTERM handler, announce readiness, then wait for the
// signal and report that it ran -- proving Process.Signal reaches the guest.
#include <signal.h>
#include <unistd.h>
static volatile sig_atomic_t got = 0;
static void onterm(int s) { (void)s; got = 1; }
int main(void) {
	sigset_t block, empty;
	sigemptyset(&block);
	sigaddset(&block, SIGTERM);
	sigprocmask(SIG_BLOCK, &block, 0);
	struct sigaction sa = {0};
	sa.sa_handler = onterm;
	sigaction(SIGTERM, &sa, 0);
	write(1, "READY", 5);
	sigemptyset(&empty);
	while (!got)
		sigsuspend(&empty); // atomically unblocks SIGTERM and waits; no race
	write(1, "GOTTERM", 7);
	return 0;
}
