// syscov.c -- syscall coverage probe. Calls each audited Linux syscall with
// benign arguments and prints one line "NAME code" per call, where code is the
// errno (0 = served, 38 = ENOSYS = not implemented on this host). The same
// static-PIE musl binary runs under qqbox on every host OS, so a diff of its
// output across linux/darwin/windows is the authoritative coverage matrix.
//
// It never asserts a result value, only whether the syscall reached a handler:
// a call that returns EINVAL/EFAULT/EPERM/ESRCH is "served"; only ENOSYS is a
// gap. syscall() returns -1 and sets errno (it is the libc wrapper), so the
// errno -- not the return value -- is what is reported.
#define _GNU_SOURCE
#include <errno.h>
#include <unistd.h>
#include <sys/syscall.h>

static void emit(const char *name, long ret) {
    int e = (ret < 0) ? errno : 0;
    char buf[128];
    int n = 0;
    for (const char *p = name; *p; p++) buf[n++] = *p;
    buf[n++] = ' ';
    char t[16]; int k = 0;
    if (e == 0) t[k++] = '0';
    else { int v = e; while (v) { t[k++] = '0' + v % 10; v /= 10; } }
    while (k) buf[n++] = t[--k];
    buf[n++] = '\n';
    (void)write(1, buf, n);
}

#define PROBE(name, ...) emit(#name, syscall(__VA_ARGS__))

int main(void) {
    static char big[1024];
    static char zero[16]; // stays zero; a timespec of {0,0} for nanosleep
    char *b = big;

    // memory advisory / info
    PROBE(madvise,          SYS_madvise, b, 0, 0);   // MADV_NORMAL
    PROBE(madvise_free,     SYS_madvise, b, 0, 8);   // MADV_FREE
    PROBE(madvise_dontneed, SYS_madvise, b, 0, 4);   // MADV_DONTNEED
    PROBE(madvise_remove,   SYS_madvise, b, 0, 9);   // MADV_REMOVE
    PROBE(msync,            SYS_msync, b, 0, 4);      // MS_SYNC
    PROBE(mincore,          SYS_mincore, b, 0, b + 512);
    PROBE(mlock,            SYS_mlock, b, 0);
    PROBE(munlock,          SYS_munlock, b, 0);
    PROBE(mlockall,         SYS_mlockall, 1);         // MCL_CURRENT
    PROBE(munlockall,       SYS_munlockall);
#ifdef SYS_mlock2
    PROBE(mlock2,           SYS_mlock2, b, 0, 0);
#endif

    // SysV IPC
    PROBE(shmget,   SYS_shmget, 0, 4096, 0x3ff);
    PROBE(shmat,    SYS_shmat, -1, 0, 0);            // bad id
    PROBE(shmdt,    SYS_shmdt, 0);
    PROBE(semget,   SYS_semget, 0, 1, 0x3ff);
    PROBE(msgget,   SYS_msgget, 0, 0x3ff);

    // thread / scheduling info
    PROBE(getcpu,           SYS_getcpu, b, b + 16, 0);
    PROBE(membarrier,       SYS_membarrier, 0, 0, 0); // CMD_QUERY
    PROBE(set_robust_list,  SYS_set_robust_list, b, 24);
    PROBE(get_robust_list,  SYS_get_robust_list, 0, b, b + 16);
    PROBE(sched_yield,      SYS_sched_yield);
    PROBE(sched_getaffinity, SYS_sched_getaffinity, 0, 8, b);
    PROBE(getrusage,        SYS_getrusage, 0, b);

    // cross-process memory / debugging
    PROBE(process_vm_readv,  SYS_process_vm_readv, 0, 0, 0, 0, 0, 0);
    PROBE(process_vm_writev, SYS_process_vm_writev, 0, 0, 0, 0, 0, 0);
    PROBE(ptrace,            SYS_ptrace, 3, 0, 0, 0);  // PTRACE_PEEKTEXT

    // time / random
    PROBE(clock_gettime, SYS_clock_gettime, 1, b);
    PROBE(getrandom,     SYS_getrandom, b, 0, 0);
    PROBE(nanosleep,     SYS_nanosleep, zero, 0);     // {0,0}: returns immediately

    // misc sanity (served everywhere)
    PROBE(uname,     SYS_uname, b);
    PROBE(sysinfo,   SYS_sysinfo, b);
    PROBE(prctl,     SYS_prctl, 7, 0, 0, 0, 0);       // PR_GET_KEEPCAPS
    PROBE(prlimit64, SYS_prlimit64, 0, 7, 0, b);
    PROBE(statfs,    SYS_statfs, (long)"/", b);

    (void)write(1, "SYSCOV-DONE\n", 12);
    return 0;
}
