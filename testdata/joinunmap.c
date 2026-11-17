// Threads on caller-owned stacks that the joiner unmaps as soon as
// pthread_join returns, as thread libraries do with cached stacks.
#include <pthread.h>
#include <stdio.h>
#include <sys/mman.h>

static void *nop(void *a) { return a; }

int main(void) {
	for (int i = 0; i < 500; i++) {
		size_t sz = 1 << 16;
		void *stk = mmap(0, sz, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		pthread_attr_t at;
		pthread_attr_init(&at);
		pthread_attr_setstack(&at, stk, sz);
		pthread_t t;
		if (pthread_create(&t, &at, nop, 0)) { printf("create failed\n"); return 1; }
		pthread_join(t, 0);
		munmap(stk, sz);
	}
	printf("joinunmap ok\n");
	return 0;
}
