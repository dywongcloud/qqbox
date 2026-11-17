#define _GNU_SOURCE
#include <stdio.h>
#include <sched.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <sys/socket.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#ifndef IFF_UP
#define IFF_UP 0x1
#endif

// Mimic crun bringing lo up: RTM_NEWLINK with NLM_F_ACK, then recvfrom the ACK.
int main(void) {
	if (unshare(CLONE_NEWNET) != 0) perror("unshare netns (non-fatal)");
	int s = socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC, NETLINK_ROUTE);
	if (s < 0) { perror("socket"); return 1; }
	struct sockaddr_nl sa = {.nl_family = AF_NETLINK};
	if (bind(s, (void *)&sa, sizeof sa) < 0) { perror("bind"); return 2; }

	struct {
		struct nlmsghdr nh;
		struct ifinfomsg ifi;
	} req;
	memset(&req, 0, sizeof req);
	req.nh.nlmsg_len = NLMSG_LENGTH(sizeof(struct ifinfomsg));
	req.nh.nlmsg_type = RTM_NEWLINK;
	req.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
	req.nh.nlmsg_seq = 1;
	req.ifi.ifi_family = AF_UNSPEC;
	req.ifi.ifi_index = 1; // lo
	req.ifi.ifi_flags = IFF_UP;
	req.ifi.ifi_change = IFF_UP;
	struct sockaddr_nl kernel = {.nl_family = AF_NETLINK};
	if (sendto(s, &req, req.nh.nlmsg_len, 0, (void *)&kernel, sizeof kernel) < 0) { perror("sendto"); return 3; }

	char buf[8192];
	struct sockaddr_nl from;
	socklen_t fromlen = sizeof from;
	ssize_t n = recvfrom(s, buf, sizeof buf, 0, (void *)&from, &fromlen);
	if (n < 0) { perror("recvfrom"); return 4; }
	printf("recvfrom n=%zd fromlen=%u from.pid=%u\n", n, fromlen, from.nl_pid);
	struct nlmsghdr *nh = (void *)buf;
	if (nh->nlmsg_type == NLMSG_ERROR) {
		struct nlmsgerr *e = NLMSG_DATA(nh);
		printf("NLMSG_ERROR err=%d (ack if 0)\n", e->error);
		printf(e->error == 0 ? "NLACK-OK\n" : "NLACK-ERRVAL\n");
	} else {
		printf("unexpected type=%u\n", nh->nlmsg_type);
	}
	return 0;
}
