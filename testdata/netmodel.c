// Proves laboon models a non-host netns's addresses: unshare a fresh network
// namespace, add 10.99.0.1/24 to lo with RTM_NEWADDR, then confirm getifaddrs
// (RTM_GETADDR under the hood) reports it. Under the old ack-and-fake netlink a
// fresh netns reported only the host's loopback, so the added address was
// invisible and this exits non-zero.
#define _GNU_SOURCE
#include <arpa/inet.h>
#include <ifaddrs.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <sched.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

int main(void) {
	if (unshare(CLONE_NEWNET) != 0) {
		perror("unshare");
		return 2;
	}
	int s = socket(AF_NETLINK, SOCK_RAW, NETLINK_ROUTE);
	if (s < 0) {
		perror("socket");
		return 2;
	}

	struct {
		struct nlmsghdr nh;
		struct ifaddrmsg ifa;
		char buf[64];
	} req;
	memset(&req, 0, sizeof(req));
	req.nh.nlmsg_len = NLMSG_LENGTH(sizeof(struct ifaddrmsg));
	req.nh.nlmsg_type = RTM_NEWADDR;
	req.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_CREATE | NLM_F_ACK;
	req.nh.nlmsg_seq = 1;
	req.ifa.ifa_family = AF_INET;
	req.ifa.ifa_prefixlen = 24;
	req.ifa.ifa_index = 1; // lo

	struct in_addr ip;
	inet_pton(AF_INET, "10.99.0.1", &ip);
	int types[2] = {IFA_LOCAL, IFA_ADDRESS};
	for (int i = 0; i < 2; i++) {
		struct rtattr *rta =
		    (struct rtattr *)((char *)&req + NLMSG_ALIGN(req.nh.nlmsg_len));
		rta->rta_type = types[i];
		rta->rta_len = RTA_LENGTH(4);
		memcpy(RTA_DATA(rta), &ip, 4);
		req.nh.nlmsg_len = NLMSG_ALIGN(req.nh.nlmsg_len) + rta->rta_len;
	}
	if (send(s, &req, req.nh.nlmsg_len, 0) < 0) {
		perror("send");
		return 2;
	}
	char ack[4096];
	int n = recv(s, ack, sizeof(ack), 0);
	if (n < 0) {
		perror("recv");
		return 2;
	}
	struct nlmsghdr *nh = (struct nlmsghdr *)ack;
	if (nh->nlmsg_type == NLMSG_ERROR) {
		struct nlmsgerr *e = (struct nlmsgerr *)NLMSG_DATA(nh);
		if (e->error != 0) {
			fprintf(stderr, "RTM_NEWADDR failed: %d\n", e->error);
			return 3;
		}
	}
	close(s);

	struct ifaddrs *ifa, *p;
	if (getifaddrs(&ifa) != 0) {
		perror("getifaddrs");
		return 2;
	}
	int found = 0;
	for (p = ifa; p; p = p->ifa_next) {
		if (p->ifa_addr && p->ifa_addr->sa_family == AF_INET) {
			char a[64];
			struct sockaddr_in *sin = (struct sockaddr_in *)p->ifa_addr;
			inet_ntop(AF_INET, &sin->sin_addr, a, sizeof(a));
			printf("iface %s addr %s\n", p->ifa_name, a);
			if (strcmp(a, "10.99.0.1") == 0)
				found = 1;
		}
	}
	freeifaddrs(ifa);
	if (!found) {
		fprintf(stderr, "configured address not visible in its netns\n");
		return 1;
	}
	printf("OK\n");
	return 0;
}
