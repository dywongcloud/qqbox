#include <sys/mman.h>
#include <unistd.h>
#include <string.h>
int main(void) {
	void *p = mmap(0, 4096, PROT_READ | PROT_WRITE | PROT_EXEC,
	               MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	const char *msg = (p == MAP_FAILED) ? "denied\n" : "allowed\n";
	write(1, msg, strlen(msg));
	return 0;
}
