// System V IPC across forked guests: shared memory segments, semaphores
// (blocking, SEM_UNDO, timeouts, removal) and message queues. Prints one line
// per check and "ALL OK" at the end. Built static-PIE, aarch64 musl.
#define _GNU_SOURCE
#include <sys/ipc.h>
#include <sys/shm.h>
#include <sys/sem.h>
#include <sys/msg.h>
#include <sys/wait.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <stdio.h>
#include <time.h>

static int fails;
#define CHECK(c, ...) do { if (c) { printf("ok   "); } else { printf("FAIL "); fails++; } printf(__VA_ARGS__); printf("\n"); } while (0)

union semun { int val; struct semid_ds *buf; unsigned short *array; };

static int waitexit(pid_t c) {
    int st;
    if (waitpid(c, &st, 0) != c) return -1;
    return WIFEXITED(st) ? WEXITSTATUS(st) : 128;
}

static void t_shm(void) {
    int id = shmget(IPC_PRIVATE, 100000, IPC_CREAT | 0600);
    CHECK(id >= 0, "shmget IPC_PRIVATE");
    int *p = shmat(id, NULL, 0);
    CHECK(p != (void *)-1, "shmat");
    p[0] = 1;
    pid_t c = fork();
    if (c == 0) {
        p[0] = 42; // inherited attachment
        int *q = shmat(id, NULL, 0);
        if (q == (void *)-1) _exit(1);
        q[1] = 43; // fresh attachment in the child
        shmdt(q);
        _exit(0);
    }
    CHECK(waitexit(c) == 0 && p[0] == 42 && p[1] == 43, "segment shared with a forked child");
    struct shmid_ds ds;
    CHECK(shmctl(id, IPC_STAT, &ds) == 0 && ds.shm_segsz == 100000 && ds.shm_nattch == 1 && ds.shm_cpid == getpid(),
          "IPC_STAT segsz=%zu nattch=%lu", ds.shm_segsz, (unsigned long)ds.shm_nattch);
    ds.shm_perm.mode = 0640;
    CHECK(shmctl(id, IPC_SET, &ds) == 0 && shmctl(id, IPC_STAT, &ds) == 0 && (ds.shm_perm.mode & 0777) == 0640, "IPC_SET mode");

    int k1 = shmget(0x1234, 5000, IPC_CREAT | 0600);
    int k2 = shmget(0x1234, 100, 0);
    CHECK(k1 >= 0 && k1 == k2, "keyed shmget finds the same segment");
    errno = 0;
    CHECK(shmget(0x1234, 100, IPC_CREAT | IPC_EXCL | 0600) == -1 && errno == EEXIST, "IPC_EXCL -> EEXIST");
    errno = 0;
    CHECK(shmget(0x1234, 9000, 0) == -1 && errno == EINVAL, "larger than the segment -> EINVAL");
    errno = 0;
    CHECK(shmget(0x9999, 100, 0) == -1 && errno == ENOENT, "missing key -> ENOENT");
    char *r = shmat(k1, NULL, SHM_RDONLY);
    CHECK(r != (void *)-1 && r[0] == 0, "SHM_RDONLY attach");

    CHECK(shmctl(id, IPC_RMID, NULL) == 0, "IPC_RMID while attached");
    p[5] = 5;
    CHECK(p[5] == 5 && shmctl(id, IPC_STAT, &ds) == 0 && (ds.shm_perm.mode & SHM_DEST), "removed segment usable until detached");
    CHECK(shmdt(p) == 0, "shmdt");
    errno = 0;
    CHECK(shmat(id, NULL, 0) == (void *)-1 && errno == EINVAL, "segment gone after last detach");
    errno = 0;
    CHECK(shmdt((void *)0x1000) == -1 && errno == EINVAL, "shmdt of a non-attachment -> EINVAL");
    struct shminfo si;
    CHECK(shmctl(0, IPC_INFO, (struct shmid_ds *)&si) >= 0 && si.shmmax > 0, "IPC_INFO");
    shmdt(r);
    shmctl(k1, IPC_RMID, NULL);
}

static void t_sem(void) {
    int id = semget(IPC_PRIVATE, 3, IPC_CREAT | 0600);
    CHECK(id >= 0, "semget");
    union semun a = {.val = 0};
    CHECK(semctl(id, 0, SETVAL, a) == 0 && semctl(id, 0, GETVAL) == 0, "SETVAL/GETVAL");
    pid_t c = fork();
    if (c == 0) {
        struct sembuf down = {0, -1, 0};
        _exit(semop(id, &down, 1) == 0 ? 0 : 1);
    }
    struct timespec ts = {0, 200000000};
    nanosleep(&ts, NULL);
    CHECK(semctl(id, 0, GETNCNT) == 1, "a child waits on the semaphore (ncnt=%d)", semctl(id, 0, GETNCNT));
    struct sembuf up = {0, 1, 0};
    CHECK(semop(id, &up, 1) == 0, "semop +1");
    CHECK(waitexit(c) == 0 && semctl(id, 0, GETVAL) == 0, "blocked child woken and decremented");
    CHECK(semctl(id, 0, GETPID) == c, "GETPID is the last operator");

    struct sembuf nw = {0, -1, IPC_NOWAIT};
    errno = 0;
    CHECK(semop(id, &nw, 1) == -1 && errno == EAGAIN, "IPC_NOWAIT -> EAGAIN");
    struct sembuf tw = {0, -1, 0};
    struct timespec to = {0, 100000000};
    errno = 0;
    CHECK(semtimedop(id, &tw, 1, &to) == -1 && errno == EAGAIN, "semtimedop timeout -> EAGAIN");

    c = fork();
    if (c == 0) {
        struct sembuf u = {1, 5, SEM_UNDO};
        semop(id, &u, 1);
        _exit(semctl(id, 1, GETVAL) == 5 ? 0 : 1);
    }
    CHECK(waitexit(c) == 0 && semctl(id, 1, GETVAL) == 0, "SEM_UNDO reverted at exit");

    unsigned short vals[3] = {1, 2, 3}, got[3] = {0};
    union semun all = {.array = vals}, gall = {.array = got};
    CHECK(semctl(id, 0, SETALL, all) == 0 && semctl(id, 0, GETALL, gall) == 0 && got[2] == 3, "SETALL/GETALL");
    struct sembuf two[2] = {{0, -1, 0}, {2, -3, 0}};
    CHECK(semop(id, two, 2) == 0 && semctl(id, 0, GETVAL) == 0 && semctl(id, 2, GETVAL) == 0, "multi-op semop is atomic");
    struct sembuf zero = {0, 0, 0};
    CHECK(semop(id, &zero, 1) == 0, "wait-for-zero on zero");
    struct semid_ds ds;
    union semun sa = {.buf = &ds};
    CHECK(semctl(id, 0, IPC_STAT, sa) == 0 && ds.sem_nsems == 3, "IPC_STAT nsems=%lu", (unsigned long)ds.sem_nsems);

    c = fork();
    if (c == 0) {
        struct sembuf down = {0, -1, 0};
        _exit(semop(id, &down, 1) == -1 && errno == EIDRM ? 0 : 1);
    }
    nanosleep(&ts, NULL);
    CHECK(semctl(id, 0, IPC_RMID) == 0, "IPC_RMID");
    CHECK(waitexit(c) == 0, "waiter gets EIDRM");
}

struct mbuf { long type; char text[64]; };

static void t_msg(void) {
    int id = msgget(IPC_PRIVATE, IPC_CREAT | 0600);
    CHECK(id >= 0, "msgget");
    struct mbuf m;
    for (int t = 1; t <= 3; t++) {
        m.type = t;
        snprintf(m.text, sizeof m.text, "msg%d", t);
        msgsnd(id, &m, strlen(m.text) + 1, 0);
    }
    struct msqid_ds ds;
    CHECK(msgctl(id, IPC_STAT, &ds) == 0 && ds.msg_qnum == 3, "three queued (qnum=%lu)", (unsigned long)ds.msg_qnum);
    CHECK(msgrcv(id, &m, sizeof m.text, 2, 0) == 5 && strcmp(m.text, "msg2") == 0, "receive by type");
    CHECK(msgrcv(id, &m, sizeof m.text, -3, 0) == 5 && m.type == 1, "negative type takes the lowest");
    CHECK(msgrcv(id, &m, sizeof m.text, 3, MSG_EXCEPT | IPC_NOWAIT) == -1 && errno == ENOMSG, "MSG_EXCEPT with only that type -> ENOMSG");
    errno = 0;
    CHECK(msgrcv(id, &m, 2, 0, 0) == -1 && errno == E2BIG, "too small -> E2BIG");
    CHECK(msgrcv(id, &m, 2, 0, MSG_NOERROR) == 2 && m.type == 3, "MSG_NOERROR truncates");
    errno = 0;
    CHECK(msgrcv(id, &m, sizeof m.text, 0, IPC_NOWAIT) == -1 && errno == ENOMSG, "empty + IPC_NOWAIT -> ENOMSG");
    pid_t c = fork();
    if (c == 0) {
        struct mbuf r;
        _exit(msgrcv(id, &r, sizeof r.text, 7, 0) == 3 && strcmp(r.text, "hi") == 0 ? 0 : 1);
    }
    struct timespec ts = {0, 100000000};
    nanosleep(&ts, NULL);
    m.type = 7;
    strcpy(m.text, "hi");
    CHECK(msgsnd(id, &m, 3, 0) == 0, "send to a blocked receiver");
    CHECK(waitexit(c) == 0, "child received it");
    errno = 0;
    m.type = 0;
    CHECK(msgsnd(id, &m, 3, 0) == -1 && errno == EINVAL, "mtype 0 -> EINVAL");
    CHECK(msgctl(id, IPC_RMID, NULL) == 0, "IPC_RMID");
    errno = 0;
    CHECK(msgsnd(id, &m, 3, 0) == -1 && errno == EINVAL, "removed queue -> EINVAL");
}

int main(void) {
    setvbuf(stdout, NULL, _IONBF, 0);
    t_shm();
    t_sem();
    t_msg();
    printf(fails ? "FAILED %d\n" : "ALL OK\n", fails);
    return fails != 0;
}
