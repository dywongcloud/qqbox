// shmtest.c -- a functional SysV shared-memory check: create a segment, attach
// it, write and read back, detach and re-attach, and confirm the data persisted
// across the detach (the segment, not the mapping, holds it). Then remove it.
// Prints SHM-OK on success.
#include <sys/ipc.h>
#include <sys/shm.h>
#include <stdio.h>
#include <string.h>

int main(void) {
    int id = shmget(IPC_PRIVATE, 4096, IPC_CREAT | 0600);
    if (id < 0) { printf("FAIL shmget\n"); return 1; }

    char *p = shmat(id, 0, 0);
    if (p == (void *)-1) { printf("FAIL shmat\n"); return 1; }
    strcpy(p, "SHM-HELLO");
    if (strcmp(p, "SHM-HELLO") != 0) { printf("FAIL readback\n"); return 1; }
    if (shmdt(p) != 0) { printf("FAIL shmdt\n"); return 1; }

    char *q = shmat(id, 0, 0);
    if (q == (void *)-1) { printf("FAIL reattach\n"); return 1; }
    if (strcmp(q, "SHM-HELLO") != 0) { printf("FAIL persist got [%s]\n", q); return 1; }
    shmdt(q);
    shmctl(id, IPC_RMID, 0);

    printf("SHM-OK\n");
    return 0;
}
