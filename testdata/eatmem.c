#include <stdlib.h>
#include <stdio.h>
#include <string.h>
int main(void) {
	size_t total = 0;
	for (int i = 0; i < 2000; i++) {
		void *p = malloc(1 << 20); // 1 MiB
		if (!p) { printf("malloc failed after %zu MiB\n", total >> 20); return 0; }
		memset(p, 1, 1 << 20);
		total += 1 << 20;
	}
	printf("allocated %zu MiB\n", total >> 20);
	return 0;
}
