// opaquedir.c — reproduces the overlay opaque-recreate sequence that the mac
// gate's fstest.c (t_dirs) exercises but the linux suite did not: empty a lower
// directory one level deep (files and a subdir of files), rmdir it, recreate it
// and confirm it is opaque (only "." and "..") with the new mode. A regression
// here means whiteout markers written into the upper leaked into the listing or
// blocked the host rmdir.
#include <dirent.h>
#include <stdio.h>
#include <sys/stat.h>
#include <unistd.h>

int main(void) {
	DIR *dd = opendir("/d");
	struct dirent *de;
	if (!dd) return 2;
	while ((de = readdir(dd))) {
		if (de->d_name[0] == '.') continue;
		char p[512];
		snprintf(p, sizeof p, "/d/%s", de->d_name);
		if (de->d_type == DT_DIR) {
			DIR *d2 = opendir(p);
			struct dirent *e2;
			while (d2 && (e2 = readdir(d2))) {
				if (e2->d_name[0] == '.') continue;
				char q[1024];
				snprintf(q, sizeof q, "%s/%s", p, e2->d_name);
				unlink(q);
			}
			if (d2) closedir(d2);
			if (rmdir(p) != 0) return 3;
		} else if (unlink(p) != 0) return 4;
	}
	closedir(dd);

	if (rmdir("/d") != 0) return 10;       // regression: ENOTEMPTY from leaked markers
	if (mkdir("/d", 0700) != 0) return 11; // regression: EEXIST (dir not removed)
	dd = opendir("/d");
	int n = 0;
	while (dd && (de = readdir(dd))) n++;
	if (dd) closedir(dd);
	if (n != 2) return 12; // opaque: only "." and ".."
	struct stat st;
	if (stat("/d", &st) != 0 || (st.st_mode & 07777) != 0700) return 13;
	return 0;
}
