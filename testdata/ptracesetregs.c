// ptracesetregs.c -- checks PTRACE_SETREGSET write-back: the tracer catches the
// child's getpid at its syscall-exit stop and overwrites the result register
// with a sentinel; the child must then see getpid() return that sentinel.
// Works on x86-64 and arm64. Prints SETREGS-OK on success.
#define _GNU_SOURCE
#include <sys/ptrace.h>
#include <sys/wait.h>
#include <sys/uio.h>
#include <sys/syscall.h>
#include <signal.h>
#include <unistd.h>
#include <stdio.h>
#include <elf.h>

#if defined(__aarch64__)
struct regs { unsigned long long x[31], sp, pc, pstate; };
#define NR_IDX 8      // x8 holds the syscall number
#define RES_IDX 0     // x0 holds the result
#define NR_getpid 172
#else
struct regs { unsigned long long r[27]; }; // x86-64 user_regs_struct
#define NR_IDX 15     // orig_rax
#define RES_IDX 10    // rax
#define NR_getpid 39
#endif

#define SENTINEL 0x4321

static unsigned long long *rp(struct regs *r) { return (unsigned long long *)r; }

int main(void) {
    pid_t c = fork();
    if (c == 0) {
        ptrace(PTRACE_TRACEME, 0, 0, 0);
        kill(getpid(), SIGSTOP);
        long pid = syscall(SYS_getpid);
        if (pid == SENTINEL) write(1, "SETREGS-OK\n", 11);
        else { char b[48]; int n = snprintf(b, sizeof b, "FAIL child saw %ld\n", pid); write(1, b, n); }
        _exit(pid == SENTINEL ? 0 : 1);
    }
    int st;
    if (waitpid(c, &st, 0) < 0 || !WIFSTOPPED(st)) { printf("FAIL initial stop\n"); return 1; }
    ptrace(PTRACE_SETOPTIONS, c, 0, PTRACE_O_TRACESYSGOOD);

    int entry = 0;
    for (int i = 0; i < 300; i++) {
        if (ptrace(PTRACE_SYSCALL, c, 0, 0) != 0) { printf("FAIL cont\n"); return 1; }
        if (waitpid(c, &st, 0) < 0) { printf("FAIL wait\n"); return 1; }
        if (WIFEXITED(st)) return WEXITSTATUS(st); // the child printed the verdict
        if (!WIFSTOPPED(st)) continue;
        struct regs r;
        struct iovec iov = { &r, sizeof(r) };
        if (ptrace(PTRACE_GETREGSET, c, NT_PRSTATUS, &iov) != 0) { printf("FAIL getregset\n"); return 1; }
        if (rp(&r)[NR_IDX] != NR_getpid) continue;
        if (!entry) { entry = 1; continue; } // the entry stop; the next is the exit
        rp(&r)[RES_IDX] = SENTINEL;
        iov.iov_len = sizeof(r);
        if (ptrace(PTRACE_SETREGSET, c, NT_PRSTATUS, &iov) != 0) { printf("FAIL setregset\n"); return 1; }
        ptrace(PTRACE_CONT, c, 0, 0);
        waitpid(c, &st, 0);
        return WIFEXITED(st) ? WEXITSTATUS(st) : 1;
    }
    printf("FAIL never saw getpid exit\n");
    return 1;
}
