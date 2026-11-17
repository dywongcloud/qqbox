// Proves the host network namespace is modelled as a live-plus-overlay view:
// create a bridge with RTM_NEWLINK (as a container runtime does on the host),
// then a link dump must return BOTH the real host loopback and the created
// bridge. Under the old ack-and-fake netlink the bridge would be invisible.
#define _GNU_SOURCE
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int create_bridge(int s, const char *name) {
	struct {
		struct nlmsghdr nh;
		struct ifinfomsg ifi;
		char buf[128];
	} req;
	memset(&req, 0, sizeof(req));
	req.nh.nlmsg_len = NLMSG_LENGTH(sizeof(struct ifinfomsg));
	req.nh.nlmsg_type = RTM_NEWLINK;
	req.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_CREATE | NLM_F_EXCL | NLM_F_ACK;
	req.nh.nlmsg_seq = 1;
	req.ifi.ifi_family = AF_UNSPEC;

	struct rtattr *rta = (struct rtattr *)((char *)&req + NLMSG_ALIGN(req.nh.nlmsg_len));
	rta->rta_type = IFLA_IFNAME;
	rta->rta_len = RTA_LENGTH(strlen(name) + 1);
	strcpy(RTA_DATA(rta), name);
	req.nh.nlmsg_len = NLMSG_ALIGN(req.nh.nlmsg_len) + rta->rta_len;

	rta = (struct rtattr *)((char *)&req + NLMSG_ALIGN(req.nh.nlmsg_len));
	rta->rta_type = IFLA_LINKINFO;
	struct rtattr *kind = (struct rtattr *)RTA_DATA(rta);
	kind->rta_type = IFLA_INFO_KIND;
	kind->rta_len = RTA_LENGTH(strlen("bridge") + 1);
	strcpy(RTA_DATA(kind), "bridge");
	rta->rta_len = RTA_LENGTH(0) + RTA_ALIGN(kind->rta_len);
	req.nh.nlmsg_len = NLMSG_ALIGN(req.nh.nlmsg_len) + rta->rta_len;

	return send(s, &req, req.nh.nlmsg_len, 0);
}

int main(void) {
	int s = socket(AF_NETLINK, SOCK_RAW, NETLINK_ROUTE);
	if (s < 0) {
		perror("socket");
		return 2;
	}
	if (create_bridge(s, "labbr0") < 0) {
		perror("send");
		return 2;
	}
	char buf[8192];
	int n = recv(s, buf, sizeof(buf), 0);
	if (n < 0) {
		perror("recv");
		return 2;
	}
	struct nlmsghdr *nh = (struct nlmsghdr *)buf;
	if (nh->nlmsg_type == NLMSG_ERROR) {
		struct nlmsgerr *e = (struct nlmsgerr *)NLMSG_DATA(nh);
		if (e->error != 0) {
			fprintf(stderr, "create bridge failed: %d\n", e->error);
			return 3;
		}
	}

	struct {
		struct nlmsghdr nh;
		struct ifinfomsg ifi;
	} dump;
	memset(&dump, 0, sizeof(dump));
	dump.nh.nlmsg_len = NLMSG_LENGTH(sizeof(struct ifinfomsg));
	dump.nh.nlmsg_type = RTM_GETLINK;
	dump.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
	dump.nh.nlmsg_seq = 2;
	if (send(s, &dump, dump.nh.nlmsg_len, 0) < 0) {
		perror("send dump");
		return 2;
	}

	int seen_lo = 0, seen_br = 0, done = 0;
	while (!done) {
		n = recv(s, buf, sizeof(buf), 0);
		if (n <= 0)
			break;
		for (nh = (struct nlmsghdr *)buf; NLMSG_OK(nh, n); nh = NLMSG_NEXT(nh, n)) {
			if (nh->nlmsg_type == NLMSG_DONE) {
				done = 1;
				break;
			}
			if (nh->nlmsg_type != RTM_NEWLINK)
				continue;
			struct ifinfomsg *ifi = NLMSG_DATA(nh);
			int rlen = IFLA_PAYLOAD(nh);
			for (struct rtattr *a = IFLA_RTA(ifi); RTA_OK(a, rlen); a = RTA_NEXT(a, rlen)) {
				if (a->rta_type == IFLA_IFNAME) {
					const char *nm = (const char *)RTA_DATA(a);
					if (!strcmp(nm, "lo"))
						seen_lo = 1;
					if (!strcmp(nm, "labbr0"))
						seen_br = 1;
				}
			}
		}
	}
	close(s);
	if (!seen_br) {
		fprintf(stderr, "created bridge missing from the link dump\n");
		return 1;
	}
	if (!seen_lo) {
		fprintf(stderr, "real host loopback missing from the link dump\n");
		return 1;
	}
	printf("OK\n");
	return 0;
}
