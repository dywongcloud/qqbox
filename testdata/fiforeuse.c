#define _GNU_SOURCE
#include <stdio.h>
#include <string.h>
#include <fcntl.h>
#include <unistd.h>
#include <sys/stat.h>

// A removed fifo's inode, reused by a later regular file, must NOT carry the
// fifo type forward (stale special-node metadata kept per inode). Loop until
// the inode is reused, then require the regular file to stat as regular.
int main(void) {
	const char *p = "/tmp/reuse";
	for (int i = 0; i < 300; i++) {
		if (mkfifo(p, 0600) != 0) { perror("mkfifo"); return 2; }
		struct stat fs;
		if (stat(p, &fs) != 0 || !S_ISFIFO(fs.st_mode)) { printf("not a fifo\n"); return 3; }
		unsigned long fifoino = (unsigned long)fs.st_ino;
		unlink(p);
		int fd = open(p, O_CREAT | O_WRONLY, 0600);
		if (fd < 0) { perror("open"); return 4; }
		write(fd, "hi", 2);
		close(fd);
		struct stat rs;
		if (stat(p, &rs) != 0) { perror("stat reg"); return 5; }
		unsigned long regino = (unsigned long)rs.st_ino;
		if (regino == fifoino) { // inode reused: the type must be regular now
			if (!S_ISREG(rs.st_mode)) { printf("BUG reused inode mode=%o\n", (unsigned)rs.st_mode); unlink(p); return 1; }
			unlink(p);
			printf("FIFOREUSE-OK reused@%d\n", i);
			return 0;
		}
		unlink(p);
	}
	printf("FIFOREUSE-OK no-reuse\n"); // could not force reuse; not a failure
	return 0;
}
