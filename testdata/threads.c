#include <stdio.h>
#include <pthread.h>
#include <stdatomic.h>

static atomic_int counter;
static void *work(void *arg) {
	long id = (long)arg;
	for (int i = 0; i < 1000; i++) atomic_fetch_add(&counter, 1);
	return (void *)(id * 10);
}

int main(void) {
	pthread_t t[4];
	for (long i = 0; i < 4; i++)
		if (pthread_create(&t[i], NULL, work, (void *)i)) { printf("create failed\n"); return 2; }
	long sum = 0;
	for (int i = 0; i < 4; i++) { void *r; pthread_join(t[i], &r); sum += (long)r; }
	printf("counter=%d joined_sum=%ld\n", atomic_load(&counter), sum);
	return 0;
}
