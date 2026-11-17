// smaps: /proc/self/smaps as Linux has it. Every mapping of /proc/self/maps,
// in order, with Linux's fields; the sizes it reports in kB adding up; a page
// the process wrote counted resident, private and dirty; after a fork, a page
// both processes still share counted shared, dirty in the child (redis's
// check for an arm64 kernel bug); and smaps_rollup the sum.
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/wait.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

// header reports whether line starts an entry ("lo-hi perms ..."), and its
// range.
static int header(const char *line, unsigned long *lo, unsigned long *hi) {
	char perms[8];
	return sscanf(line, "%lx-%lx %7s", lo, hi, perms) == 3 && strlen(perms) == 4;
}

// field is the kB value of field name in the smaps entry holding addr, -1 if
// none.
static long field(const char *path, unsigned long addr, const char *name) {
	FILE *f = fopen(path, "r");
	if (!f)
		return -1;
	char buf[512];
	int in = 0;
	long v = -1;
	size_t nl = strlen(name);
	while (fgets(buf, sizeof buf, f)) {
		unsigned long lo, hi;
		if (header(buf, &lo, &hi)) {
			in = lo <= addr && addr < hi;
			continue;
		}
		if (in && !strncmp(buf, name, nl) && buf[nl] == ':') {
			sscanf(buf + nl + 1, "%ld", &v);
			break;
		}
	}
	fclose(f);
	return v;
}

static const char *fields[] = {"Size", "KernelPageSize", "MMUPageSize", "Rss", "Pss", "Shared_Clean", "Shared_Dirty",
	"Private_Clean", "Private_Dirty", "Referenced", "Anonymous", "Swap", "SwapPss", "Locked", "VmFlags"};

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	long page = sysconf(_SC_PAGESIZE);

	// smaps lists maps' entries, in order, each with every field.
	FILE *m = fopen("/proc/self/maps", "r"), *s = fopen("/proc/self/smaps", "r");
	CHECK(m && s, "open maps %p smaps %p: %s", (void *)m, (void *)s, strerror(errno));
	if (!m || !s)
		goto out;
	// Read whole first: the files change as the process allocates.
	static char mbuf[1 << 20], sbuf[4 << 20];
	size_t mn = fread(mbuf, 1, sizeof mbuf - 1, m), sn = fread(sbuf, 1, sizeof sbuf - 1, s);
	mbuf[mn] = sbuf[sn] = 0;
	int entries = 0, headers = 0, seen[16] = {0}, bad = 0;
	char *sp = sbuf;
	for (char *ml = strtok(mbuf, "\n"); ml; ml = strtok(NULL, "\n"))
		entries++;
	// The smaps headers, in order, against the maps lines.
	char *mcur = mbuf;
	for (char *line = sp; *line; ) {
		char *nl = strchr(line, '\n');
		if (nl)
			*nl = 0;
		unsigned long lo, hi;
		if (header(line, &lo, &hi)) {
			bad += headers >= entries || strcmp(line, mcur) != 0;
			mcur += strlen(mcur) + 1;
			headers++;
		} else {
			for (int i = 0; i < 15; i++) {
				size_t n = strlen(fields[i]);
				if (!strncmp(line, fields[i], n) && line[n] == ':')
					seen[i]++;
			}
		}
		if (!nl)
			break;
		line = nl + 1;
	}
	CHECK(entries > 0 && headers == entries && bad == 0, "smaps has %d entries, maps %d, %d differ", headers, entries, bad);
	for (int i = 0; i < 15; i++)
		CHECK(seen[i] == headers, "field %s in %d of %d entries", fields[i], seen[i], headers);
	fclose(m);
	fclose(s);

	// A page written is resident, private and dirty; the mapping's size is
	// its length.
	char *p = mmap(NULL, 3 * page, PROT_READ, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	char *q = p + page;
	mprotect(q, page, PROT_READ | PROT_WRITE); // a mapping of its own, as redis splits it
	*(volatile char *)q = 1;
	unsigned long a = (unsigned long)q;
	CHECK(field("/proc/self/smaps", a, "Size") == page / 1024, "Size %ld", field("/proc/self/smaps", a, "Size"));
	CHECK(field("/proc/self/smaps", a, "Rss") == page / 1024, "Rss %ld", field("/proc/self/smaps", a, "Rss"));
	CHECK(field("/proc/self/smaps", a, "Private_Dirty") == page / 1024, "Private_Dirty %ld", field("/proc/self/smaps", a, "Private_Dirty"));
	CHECK(field("/proc/self/smaps", a, "Anonymous") == page / 1024, "Anonymous %ld", field("/proc/self/smaps", a, "Anonymous"));

	// redis's check: MADV_FREE, written again, then a fork: the child sees
	// the page shared and dirty.
	int e = madvise(q, page, 8 /* MADV_FREE */) == 0 ? 0 : errno;
	CHECK(e == 0, "MADV_FREE: %s", strerror(e));
	*(volatile char *)q = 2;
	int pfd[2];
	if (pipe(pfd) != 0)
		return 2;
	pid_t pid = fork();
	if (pid == 0) {
		long v = field("/proc/self/smaps", a, "Shared_Dirty");
		if (v <= 0) { // what the child sees of the page, for the failure below
			FILE *f = fopen("/proc/self/smaps", "r");
			char l[256];
			int in = 0;
			while (f && fgets(l, sizeof l, f)) {
				unsigned long lo, hi;
				if (header(l, &lo, &hi))
					in = lo <= a && a < hi;
				if (in)
					printf("  child: %s", l);
			}
		}
		if (write(pfd[1], &v, sizeof v) != sizeof v)
			_exit(1);
		_exit(0);
	}
	long sd = -1;
	if (read(pfd[0], &sd, sizeof sd) != sizeof sd)
		sd = -1;
	waitpid(pid, NULL, 0);
	CHECK(sd > 0, "Shared_Dirty in the fork child: %ld", sd);

	// smaps_rollup: one entry, its Rss the sum of smaps'.
	long sum = 0;
	FILE *f = fopen("/proc/self/smaps", "r");
	char buf[512];
	while (f && fgets(buf, sizeof buf, f)) {
		long v;
		if (!strncmp(buf, "Rss:", 4) && sscanf(buf + 4, "%ld", &v) == 1)
			sum += v;
	}
	if (f)
		fclose(f);
	long roll = field("/proc/self/smaps_rollup", 0, "Rss");
	FILE *r = fopen("/proc/self/smaps_rollup", "r");
	unsigned long rlo = 1, rhi = 0;
	if (r && fgets(buf, sizeof buf, r))
		sscanf(buf, "%lx-%lx", &rlo, &rhi);
	if (r)
		fclose(r);
	CHECK(rlo == 0 || rlo < rhi, "smaps_rollup's range %lx-%lx", rlo, rhi);
	roll = -1;
	r = fopen("/proc/self/smaps_rollup", "r");
	while (r && fgets(buf, sizeof buf, r))
		if (!strncmp(buf, "Rss:", 4))
			sscanf(buf + 4, "%ld", &roll);
	if (r)
		fclose(r);
	CHECK(roll > 0 && roll >= sum - sum / 10 && roll <= sum + sum / 10, "smaps_rollup Rss %ld, smaps' sum %ld", roll, sum);
out:
	printf("smaps fails=%d\n", fails);
	return fails != 0;
}
