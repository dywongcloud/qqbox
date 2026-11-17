// getdentsdir exercises directory-fd identity in getdents64: a fd opened on a
// directory must keep enumerating the directory it was opened on, not whatever
// later takes its path. It checks three things, each on a held fd:
//   replaceHidesImpostor: the opened dir's path is replaced by a different
//     directory (rename-onto) -> the held fd must not list the impostor, on the
//     first read and again after rewind.
//   moveFollows: the opened dir is itself renamed -> the held fd still lists its
//     own entries (laboon retargets the description to the new path).
//   copyUpStable: a write into a lower-layer dir copies it up (new backing
//     inode) -> the held fd still lists both the lower entry and the new one.
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/syscall.h>
#include <sys/stat.h>

struct de64 {
	unsigned long long ino;
	long long off;
	unsigned short reclen;
	unsigned char type;
	char name[];
};

// has reports whether a getdents64 stream over fd contains an entry named want
// (ignoring "." and ".."). The fd is rewound first so repeated checks are fresh.
static int has(int fd, const char *want) {
	lseek(fd, 0, SEEK_SET);
	char buf[8192];
	for (;;) {
		long n = syscall(SYS_getdents64, fd, buf, sizeof buf);
		if (n <= 0)
			return 0;
		for (long o = 0; o < n;) {
			struct de64 *d = (struct de64 *)(buf + o);
			if (strcmp(d->name, want) == 0)
				return 1;
			o += d->reclen;
		}
	}
}

static void mkfile(const char *p) {
	int fd = open(p, O_WRONLY | O_CREAT | O_TRUNC, 0644);
	if (fd >= 0)
		close(fd);
}

int main(void) {
	// Case A: replace the opened directory's path with a different directory.
	mkdir("/A", 0755);
	int a = open("/A", O_RDONLY | O_DIRECTORY);
	mkdir("/X", 0755);
	mkfile("/X/IMPOSTOR");
	rename("/X", "/A"); // /A was empty, so its inode is replaced by /X's
	int leak = has(a, "IMPOSTOR");
	int leak2 = has(a, "IMPOSTOR"); // again, exercising rewind in has()
	printf("replaceHidesImpostor=%d\n", leak == 0 && leak2 == 0);

	// Case B: rename the opened directory itself; the fd must follow it.
	mkdir("/C", 0755);
	mkfile("/C/KEEP");
	int c = open("/C", O_RDONLY | O_DIRECTORY);
	rename("/C", "/D");
	printf("moveFollows=%d\n", has(c, "KEEP"));

	// Case C: a lower-layer directory copied up by a write must still enumerate
	// its lower entry together with the new one.
	int p = open("/pre", O_RDONLY | O_DIRECTORY);
	if (p >= 0) {
		mkfile("/pre/up"); // first write under /pre copies the dir up
		printf("copyUpStable=%d\n", has(p, "lo") && has(p, "up"));
		close(p);
	} else {
		printf("copyUpStable=skip\n");
	}

	close(a);
	close(c);
	printf("GETDENTS-DONE\n");
	return 0;
}
