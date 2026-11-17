/* Counts CPUs the way libuv's uv_cpu_info does: cpuN lines in /proc/stat,
   processor entries in /proc/cpuinfo, plus /sys/devices/system/cpu/online. */
#include <stdio.h>
#include <string.h>

int main(void) {
	char line[4096];
	int stat = 0, info = 0;
	FILE *f = fopen("/proc/stat", "r");
	if (!f) return 1;
	while (fgets(line, sizeof line, f))
		if (!strncmp(line, "cpu", 3) && line[3] >= '0' && line[3] <= '9') stat++;
	fclose(f);
	if (!(f = fopen("/proc/cpuinfo", "r"))) return 2;
	while (fgets(line, sizeof line, f))
		if (!strncmp(line, "processor", 9)) info++;
	fclose(f);
	if (!(f = fopen("/sys/devices/system/cpu/online", "r")) || !fgets(line, sizeof line, f)) return 3;
	printf("stat=%d cpuinfo=%d online=%s", stat, info, line);
	return 0;
}
