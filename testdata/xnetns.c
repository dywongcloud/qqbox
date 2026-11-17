#define _GNU_SOURCE
#include <arpa/inet.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <netinet/in.h>
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>
// Two containers in SEPARATE network namespaces reach each other over the shared
// virtual network: a server on 10.88.0.2:9000 in netns A is reached by a client
// in netns B. Each namespace first CONFIGURES its own address with RTM_NEWADDR,
// exactly as a container runtime does -- binding an address the namespace does
// not hold is EADDRNOTAVAIL on Linux, and the virtual network's membership comes
// from the addresses the guest configures, not a fixed subnet.

// cfg_addr adds ip/prefix to the loopback interface (index 1) of the current
// network namespace. Returns 0 on success.
static int cfg_addr(const char *ip, int prefix) {
	int s = socket(AF_NETLINK, SOCK_RAW, NETLINK_ROUTE);
	if (s < 0) return -1;
	struct {
		struct nlmsghdr nh;
		struct ifaddrmsg ifa;
		char buf[64];
	} req;
	memset(&req, 0, sizeof req);
	req.nh.nlmsg_len = NLMSG_LENGTH(sizeof(struct ifaddrmsg));
	req.nh.nlmsg_type = RTM_NEWADDR;
	req.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_CREATE | NLM_F_ACK;
	req.nh.nlmsg_seq = 1;
	req.ifa.ifa_family = AF_INET;
	req.ifa.ifa_prefixlen = prefix;
	req.ifa.ifa_index = 1; // lo
	struct in_addr a;
	inet_pton(AF_INET, ip, &a);
	int types[2] = {IFA_LOCAL, IFA_ADDRESS};
	for (int i = 0; i < 2; i++) {
		struct rtattr *rta = (struct rtattr *)((char *)&req + NLMSG_ALIGN(req.nh.nlmsg_len));
		rta->rta_type = types[i];
		rta->rta_len = RTA_LENGTH(4);
		memcpy(RTA_DATA(rta), &a, 4);
		req.nh.nlmsg_len = NLMSG_ALIGN(req.nh.nlmsg_len) + rta->rta_len;
	}
	if (send(s, &req, req.nh.nlmsg_len, 0) < 0) { close(s); return -1; }
	char ack[1024];
	int n = recv(s, ack, sizeof ack, 0);
	close(s);
	if (n < 0) return -1;
	struct nlmsghdr *nh = (struct nlmsghdr *)ack;
	if (nh->nlmsg_type == NLMSG_ERROR) {
		struct nlmsgerr *e = (struct nlmsgerr *)NLMSG_DATA(nh);
		if (e->error != 0) return e->error;
	}
	return 0;
}

static void addr(struct sockaddr_in *a, const char *ip, int port) {
	memset(a, 0, sizeof *a);
	a->sin_family = AF_INET;
	a->sin_port = htons(port);
	inet_pton(AF_INET, ip, &a->sin_addr);
}

int main(void) {
	int rdy[2];
	if (pipe(rdy) != 0) { perror("pipe"); return 1; }

	pid_t a = fork();
	if (a == 0) { // server, netns A
		close(rdy[0]);
		if (unshare(CLONE_NEWNET) != 0) { perror("A unshare"); _exit(31); }
		if (cfg_addr("10.88.0.2", 16) != 0) { perror("A addr"); _exit(30); }
		int ls = socket(AF_INET, SOCK_STREAM, 0);
		struct sockaddr_in s; addr(&s, "10.88.0.2", 9000);
		if (bind(ls, (void *)&s, sizeof s) != 0) { perror("A bind"); _exit(32); }
		if (listen(ls, 8) != 0) { perror("A listen"); _exit(33); }
		char one = 1; write(rdy[1], &one, 1); close(rdy[1]);
		int c = accept(ls, NULL, NULL);
		if (c < 0) { perror("A accept"); _exit(34); }
		char b[8] = {0};
		if (read(c, b, 8) != 4 || memcmp(b, "ping", 4)) { fprintf(stderr, "A got %s\n", b); _exit(35); }
		write(c, "pong", 4);
		_exit(0);
	}
	close(rdy[1]);
	char one; read(rdy[0], &one, 1); // wait until A is listening

	pid_t b = fork();
	if (b == 0) { // client, netns B
		if (unshare(CLONE_NEWNET) != 0) { perror("B unshare"); _exit(41); }
		if (cfg_addr("10.88.0.3", 16) != 0) { perror("B addr"); _exit(40); }
		int cs = socket(AF_INET, SOCK_STREAM, 0);
		struct sockaddr_in d; addr(&d, "10.88.0.2", 9000);
		int ok = -1;
		for (int i = 0; i < 200 && ok != 0; i++) ok = connect(cs, (void *)&d, sizeof d);
		if (ok != 0) { perror("B connect"); _exit(42); }
		if (write(cs, "ping", 4) != 4) { perror("B write"); _exit(43); }
		char r[8] = {0};
		if (read(cs, r, 8) != 4 || memcmp(r, "pong", 4)) { fprintf(stderr, "B got %s\n", r); _exit(44); }
		_exit(0);
	}
	int sa = 0, sb = 0;
	waitpid(a, &sa, 0); waitpid(b, &sb, 0);
	if (!WIFEXITED(sa) || WEXITSTATUS(sa) || !WIFEXITED(sb) || WEXITSTATUS(sb)) {
		fprintf(stderr, "A=%d B=%d\n", sa, sb); return 5;
	}
	printf("XNETNS-OK\n");
	return 0;
}
