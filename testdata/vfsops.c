// vfsops.c — a guest that exercises the overlay: create a file and a directory,
// a symlink, delete a lower file (a whiteout) and chmod a lower file (a
// copy-up). A test then checks a vfs.Stack over the run's lower and upper
// directories reflects all of it — the safety net for porting the live overlay
// onto the stack.
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>

int main(void) {
	int fd = open("/new.txt", O_CREAT | O_WRONLY, 0640);
	if (fd < 0) return 10;
	write(fd, "hello", 5);
	close(fd);
	if (mkdir("/d", 0755) != 0) return 11;
	if (symlink("/keep.txt", "/link") != 0) return 12;
	if (unlink("/gone.txt") != 0) return 13; // lower file -> whiteout
	if (chmod("/keep.txt", 0600) != 0) return 14; // lower file -> copy-up
	return 0;
}
