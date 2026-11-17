// exit_group while other threads are mapping and unmapping memory.
#include <pthread.h>
#include <stdlib.h>
#include <sys/mman.h>

static void *churn(void *a) {
	for (;;) {
		void *p = mmap(0, 4096, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		munmap(p, 4096);
	}
	return a;
}

int main(void) {
	pthread_t t;
	for (int i = 0; i < 4; i++) pthread_create(&t, 0, churn, 0);
	for (volatile int i = 0; i < 2000000; i++) ;
	exit(3);
}
