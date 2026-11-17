// selfstore: code that stores into the page it runs from, in memory mapped
// writable and executable: private anonymous memory, shared anonymous memory
// and a memfd mapped shared (read back through a second mapping, to see the
// store reached the memory). Each store form (a plain store, a pair, an
// unscaled offset, a pre-index writeback, a vector store, a cache-block zero,
// atomics) writes and reads back; a load/store-exclusive loop on the page
// counts exactly from four threads at once; stores from elsewhere land while
// other threads run the page; and code writes a new instruction into its own
// page and runs it.
#define _GNU_SOURCE
#include <pthread.h>
#include <setjmp.h>
#include <signal.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

typedef long (*fn)(long);

// A blob is code with the data it stores into on its own page, at 64,
// filled with init first: called with arg, it returns want, and the data
// word at 64+at then holds mem.
struct blob {
	const char *name;
	const unsigned char *code;
	size_t len;
	unsigned char init;
	long arg, want;
	size_t at;
	long mem;
};

#if defined(__aarch64__)
#define W(x) (unsigned char)(x), (unsigned char)((x) >> 8), (unsigned char)((x) >> 16), (unsigned char)((x) >> 24)
#define NOP W(0xd503201f)
#define RET W(0xd65f03c0)
// adr x1, #64; <store>; ldr x0, [x1]; ret — data at 64.
#define STORE(s) W(0x10000201), W(s), W(0xf9400020), RET
static const unsigned char str_[] = {STORE(0xf9000020)};      // str x0, [x1]
static const unsigned char stp_[] = {STORE(0xa9000020)};      // stp x0, x0, [x1]
static const unsigned char stur_[] = {W(0x10000201), W(0xf8008020), W(0xf9400420), RET}; // stur x0, [x1, #8]; ldr x0, [x1, #8]
static const unsigned char pre_[] = {STORE(0xf8008c20)};      // str x0, [x1, #8]!
static const unsigned char vec_[] = {W(0x10000201), W(0x9e670000), W(0x3d800020), W(0xf9400020), RET}; // fmov d0, x0; str q0, [x1]
static const unsigned char zva_[] = {STORE(0xd50b7421)};      // dc zva, x1
static const unsigned char add_[] = {W(0x10000201), W(0xf8200022), W(0xf9400020), RET}; // ldadd x0, x2, [x1]
static const unsigned char swp_[] = {W(0x10000201), W(0xf8208022), W(0xaa0203e0), RET}; // swp x0, x2, [x1]; mov x0, x2
// A load/store-exclusive loop adding x0 to the data word: adr x1, #64;
// 1: ldxr x2, [x1]; add x2, x2, x0; stxr w3, x2, [x1]; cbnz w3, 1b;
// mov x0, x2; ret
static const unsigned char llsc_[] = {W(0x10000201), W(0xc85f7c22), W(0x8b000042), W(0xc8037c22), W(0x35ffffa3), W(0xaa0203e0), RET};
// Writes the instruction in w0 at 28 and runs it: adr x1, #28;
// str w0, [x1]; dc cvau, x1; dsb ish; ic ivau, x1; dsb ish; isb; <28>; ret
static const unsigned char smc_[] = {W(0x100000e1), W(0xb9000020), W(0xd50b7b21), W(0xd5033b9f), W(0xd50b7521), W(0xd5033b9f), W(0xd5033fdf), NOP, RET};
static long movw0(int k) { return 0x52800000 | k << 5; } // mov w0, #k
#define LLSC_DATA 64
#elif defined(__x86_64__)
// lea rcx, [rip+57]; <store>; mov rax, [rcx]; ret — data at 64.
#define LEA 0x48, 0x8d, 0x0d, 57, 0, 0, 0
static const unsigned char str_[] = {LEA, 0x48, 0x89, 0x39, 0x48, 0x8b, 0x01, 0xc3};             // mov [rcx], rdi
static const unsigned char add_[] = {LEA, 0xf0, 0x48, 0x0f, 0xc1, 0x39, 0x48, 0x8b, 0x01, 0xc3}; // lock xadd [rcx], rdi
// lea rcx, [rip+57]; mov rax, rdi; lock xadd [rcx], rax; add rax, rdi; ret
static const unsigned char llsc_[] = {LEA, 0x48, 0x89, 0xf8, 0xf0, 0x48, 0x0f, 0xc1, 0x01, 0x48, 0x01, 0xf8, 0xc3};
// Writes edi as the immediate of the mov at 9 and runs it: lea rcx,
// [rip+3]; mov [rcx], edi; mov eax, imm32; ret
static const unsigned char smc_[] = {0x48, 0x8d, 0x0d, 3, 0, 0, 0, 0x89, 0x39, 0xb8, 0, 0, 0, 0, 0xc3};
static long movw0(int k) { return k; }
#define LLSC_DATA 64
#endif
#if defined(__aarch64__)
static const unsigned char ret_[] = {RET};
// Atomically adds 1 to the word the first argument points at, in a loop:
// 1: ldxr x2, [x0]; add x2, x2, #1; stxr w3, x2, [x0]; cbnz w3, 1b; ret
static const unsigned char inc_[] = {W(0xc85f7c02), W(0x91000442), W(0xc8037c02), W(0x35ffffa3), RET};
#else
static const unsigned char ret_[] = {0xc3};
static const unsigned char inc_[] = {0xf0, 0x48, 0xff, 0x07, 0xc3}; // lock inc qword [rdi]; ret
#endif

static const struct blob blobs[] = {
	{"str", str_, sizeof str_, 0, 0x1234, 0x1234, 0, 0x1234},
#if defined(__aarch64__)
	{"stp", stp_, sizeof stp_, 0, 0x2345, 0x2345, 8, 0x2345},
	{"stur", stur_, sizeof stur_, 0, 0x3456, 0x3456, 8, 0x3456},
	{"pre-index", pre_, sizeof pre_, 0, 0x4567, 0x4567, 8, 0x4567},
	{"vector", vec_, sizeof vec_, 0xee, 0x5678, 0x5678, 8, 0},
	{"zero block", zva_, sizeof zva_, 0xee, 0x6789, 0, 56, 0},
	{"ldadd", add_, sizeof add_, 0, 5, 5, 0, 5},
	{"swp", swp_, sizeof swp_, 0, 7, 0, 0, 7},
#else
	{"xadd", add_, sizeof add_, 0, 5, 5, 0, 5},
#endif
};

// syncCode makes the code just written at p the code that runs there, as
// the architecture asks (Zig's compiler-rt __clear_cache leaves out the last
// barrier).
static void syncCode(void *p, size_t n) {
#if defined(__aarch64__)
	unsigned long ctr;
	__asm__ volatile("mrs %0, ctr_el0" : "=r"(ctr));
	unsigned long d = 4UL << (ctr >> 16 & 15), i = 4UL << (ctr & 15);
	unsigned long lo = (unsigned long)p, hi = lo + n;
	if (!(ctr >> 28 & 1))
		for (unsigned long a = lo & ~(d - 1); a < hi; a += d)
			__asm__ volatile("dc cvau, %0" ::"r"(a) : "memory");
	__asm__ volatile("dsb ish" ::: "memory");
	if (!(ctr >> 29 & 1))
		for (unsigned long a = lo & ~(i - 1); a < hi; a += i)
			__asm__ volatile("ic ivau, %0" ::"r"(a) : "memory");
	__asm__ volatile("dsb ish\n\tisb" ::: "memory");
#else
	(void)p, (void)n;
#endif
}

// zvaOK reports whether DC ZVA may be used (DCZID_EL0.DZP clear), as code
// that uses it checks first. Where it is prohibited it may trap (SIGILL), or
// run.
static int zvaOK(void) {
#if defined(__aarch64__)
	unsigned long z;
	__asm__ volatile("mrs %0, dczid_el0" : "=r"(z));
	return !(z >> 4 & 1);
#else
	return 1;
#endif
}

static sigjmp_buf illJmp;
static void onIll(int s) { siglongjmp(illJmp, 1); }

// call runs x with arg into *got, and reports whether it ran without SIGILL.
static int call(unsigned char *x, long arg, long *got) {
	struct sigaction sa = {0}, old;
	sa.sa_handler = onIll;
	sigaction(SIGILL, &sa, &old);
	int ok = !sigsetjmp(illJmp, 1);
	if (ok)
		*got = ((fn)x)(arg);
	sigaction(SIGILL, &old, NULL);
	return ok;
}

static void load(unsigned char *x, const unsigned char *code, size_t len) {
	memset(x, 0, 4096);
	memcpy(x, code, len);
	syncCode(x, len);
}

struct llsc {
	unsigned char *x;
	long n;
};

static volatile int stopRunning;

static void *running(void *arg) {
	while (!stopRunning)
		((fn)arg)(0);
	return NULL;
}

static void *count(void *arg) {
	struct llsc *l = arg;
	for (long i = 0; i < l->n; i++)
		((fn)l->x)(1);
	return NULL;
}

struct incer {
	unsigned char *code; // inc_, on a page of its own
	void *data;          // the word it adds to, in a page other threads run
	long n;
};

static void *inccount(void *arg) {
	struct incer *in = arg;
	for (long i = 0; i < in->n; i++)
		((fn)in->code)((long)in->data);
	return NULL;
}

// one runs every case in x, mapped writable and executable, whose memory
// alias (if not NULL) maps too.
static void one(const char *kind, unsigned char *x, volatile unsigned char *alias) {
	for (size_t i = 0; i < sizeof blobs / sizeof blobs[0]; i++) {
		const struct blob *b = &blobs[i];
		load(x, b->code, b->len);
		memset(x + 64, b->init, 64);
		long got = 0;
		if (!call(x, b->arg, &got)) {
			CHECK(!strcmp(b->name, "zero block") && !zvaOK(), "%s %s: SIGILL", kind, b->name);
			continue;
		}
		CHECK(got == b->want, "%s %s: returned %#lx, want %#lx", kind, b->name, got, b->want);
		long m;
		memcpy(&m, x + 64 + b->at, sizeof m);
		CHECK(m == b->mem, "%s %s: the data holds %#lx", kind, b->name, m);
		if (alias) {
			memcpy(&m, (const void *)(alias + 64 + b->at), sizeof m);
			CHECK(m == b->mem, "%s %s: the memory holds %#lx", kind, b->name, m);
		}
	}

	// A load/store-exclusive loop on its own page counts exactly.
	load(x, llsc_, sizeof llsc_);
	memset(x + LLSC_DATA, 0, 8);
	struct llsc l = {x, 400};
	pthread_t t[4];
	for (int i = 0; i < 4; i++)
		pthread_create(&t[i], NULL, count, &l);
	for (int i = 0; i < 4; i++)
		pthread_join(t[i], NULL);
	long c;
	memcpy(&c, x + LLSC_DATA, sizeof c);
	CHECK(c == 1600, "%s exclusive count: %ld of 1600", kind, c);
	if (alias) {
		memcpy(&c, (const void *)(alias + LLSC_DATA), sizeof c);
		CHECK(c == 1600, "%s exclusive count in the memory: %ld", kind, c);
	}

	// Stores from code elsewhere into a page other threads keep running all
	// land, each seen at once through the memory's other mapping.
	load(x, ret_, sizeof ret_);
	stopRunning = 0;
	for (int i = 0; i < 3; i++)
		pthread_create(&t[i], NULL, running, x);
	int seen = 0;
	for (long i = 1; i <= 200; i++) {
		*(volatile long *)(x + 128) = i;
		if (alias && *(volatile long *)(alias + 128) == i)
			seen++;
	}
	stopRunning = 1;
	for (int i = 0; i < 3; i++)
		pthread_join(t[i], NULL);
	CHECK(*(volatile long *)(x + 128) == 200, "%s stores beside running code: %ld", kind, *(volatile long *)(x + 128));
	CHECK(!alias || seen == 200, "%s stores seen through the memory: %d of 200", kind, seen);

	// Atomic adds from code on another page, into a page other threads run,
	// count exactly: an x86 lock add faults and runs held; an arm64 store
	// exclusive loses its monitor to the fault and retakes it, its window run
	// from the load exclusive.
	unsigned char *code = mmap(NULL, 4096, PROT_READ | PROT_WRITE | PROT_EXEC, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	if (code != MAP_FAILED) {
		load(code, inc_, sizeof inc_);
		load(x, ret_, sizeof ret_);
		memset(x + 128, 0, 8);
		stopRunning = 0;
		for (int i = 0; i < 2; i++)
			pthread_create(&t[i], NULL, running, x);
		struct incer in = {code, x + 128, 1000};
		for (int i = 2; i < 4; i++)
			pthread_create(&t[i], NULL, inccount, &in);
		for (int i = 2; i < 4; i++)
			pthread_join(t[i], NULL);
		stopRunning = 1;
		for (int i = 0; i < 2; i++)
			pthread_join(t[i], NULL);
		long ic;
		memcpy(&ic, x + 128, sizeof ic);
		CHECK(ic == 2000, "%s atomic from elsewhere: %ld of 2000", kind, ic);
		if (alias) {
			memcpy(&ic, (const void *)(alias + 128), sizeof ic);
			CHECK(ic == 2000, "%s atomic from elsewhere in the memory: %ld", kind, ic);
		}
		munmap(code, 4096);
	}

	// Code that writes an instruction into its page runs it.
	load(x, smc_, sizeof smc_);
	CHECK(((fn)x)(movw0(42)) == 42, "%s self-modified code returned %ld", kind, ((fn)x)(movw0(42)));
	CHECK(((fn)x)(movw0(43)) == 43, "%s rewritten again returned %ld", kind, ((fn)x)(movw0(43)));
}

int main(void) {
	setvbuf(stdout, NULL, _IONBF, 0);
	long page = sysconf(_SC_PAGESIZE);
	const int rwx = PROT_READ | PROT_WRITE | PROT_EXEC;

	unsigned char *p = mmap(NULL, page, rwx, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
	CHECK(p != MAP_FAILED, "private mapping");
	if (p != MAP_FAILED)
		one("private", p, NULL);

	unsigned char *s = mmap(NULL, page, rwx, MAP_SHARED | MAP_ANONYMOUS, -1, 0);
	CHECK(s != MAP_FAILED, "shared mapping");
	if (s != MAP_FAILED)
		one("shared", s, mremap(s, 0, page, MREMAP_MAYMOVE));

	int m = memfd_create("selfstore", 0);
	ftruncate(m, page);
	unsigned char *f = mmap(NULL, page, rwx, MAP_SHARED, m, 0);
	unsigned char *fa = mmap(NULL, page, PROT_READ, MAP_SHARED, m, 0);
	CHECK(f != MAP_FAILED && fa != MAP_FAILED, "memfd mappings");
	if (f != MAP_FAILED && fa != MAP_FAILED)
		one("memfd", f, fa);

	printf("selfstore fails=%d\n", fails);
	return fails != 0;
}
