// nldump: a netlink dump comes as Linux hands it out. The first datagram is
// filled as the request is answered, at most NLMSG_GOODSIZE (a page, at most
// 8 KiB, less the kernel's skb overhead) on a socket never read from; each
// later one is filled as the reader receives, as large as the largest buffer
// it has received with, and NLMSG_DONE comes alone. A dump asked for with
// NLM_F_ACK is not acked, a second dump asked for while one runs fails with
// EBUSY, and an error echoes the whole request.
#include <arpa/inet.h>
#include <errno.h>
#include <linux/netfilter.h>
#include <linux/netfilter/nf_tables.h>
#include <linux/netfilter/nfnetlink.h>
#include <linux/netlink.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

static int fails;
#define CHECK(c, ...) do { if (!(c)) { fails++; printf("FAIL %d: ", __LINE__); printf(__VA_ARGS__); printf("\n"); } } while (0)

#define TABLES 300
#define SKB_OVERHEAD 320

static char buf[1 << 20];

// msg appends a netfilter message of type t to buf at off, with a table name
// attribute when name is given, and returns the new offset.
static int msg(int off, int type, int flags, int seq, int family, int resid, const char *name) {
	struct nlmsghdr *h = (struct nlmsghdr *)(buf + off);
	h->nlmsg_type = type;
	h->nlmsg_flags = NLM_F_REQUEST | flags;
	h->nlmsg_seq = seq;
	h->nlmsg_pid = 0;
	struct nfgenmsg *g = NLMSG_DATA(h);
	g->nfgen_family = family;
	g->version = NFNETLINK_V0;
	g->res_id = htons(resid);
	int len = NLMSG_LENGTH(sizeof *g);
	if (name) {
		struct nlattr *a = (struct nlattr *)(buf + off + len);
		a->nla_type = NFTA_TABLE_NAME;
		a->nla_len = NLA_HDRLEN + strlen(name) + 1;
		strcpy((char *)a + NLA_HDRLEN, name);
		len += NLA_ALIGN(a->nla_len);
	}
	h->nlmsg_len = len;
	return off + NLMSG_ALIGN(len);
}

int main(void) {
	long page = sysconf(_SC_PAGESIZE);
	int good = (page < 8192 ? page : 8192) - SKB_OVERHEAD;
	int s = socket(AF_NETLINK, SOCK_RAW, NETLINK_NETFILTER);
	if (s < 0) {
		printf("no NETLINK_NETFILTER: %d\nnldump fails=0\n", errno);
		return 0;
	}
	// One batch makes the tables.
	int off = msg(0, NFNL_MSG_BATCH_BEGIN, 0, 1, AF_UNSPEC, NFNL_SUBSYS_NFTABLES, 0);
	for (int i = 0; i < TABLES; i++) {
		char name[16];
		snprintf(name, sizeof name, "nldump%03d", i);
		off = msg(off, NFNL_SUBSYS_NFTABLES << 8 | NFT_MSG_NEWTABLE, NLM_F_CREATE, 2 + i, NFPROTO_INET, 0, name);
	}
	off = msg(off, NFNL_MSG_BATCH_END, 0, 2 + TABLES, AF_UNSPEC, NFNL_SUBSYS_NFTABLES, 0);
	CHECK(send(s, buf, off, 0) == off, "send the batch: errno %d", errno);
	ssize_t n = recv(s, buf, sizeof buf, MSG_DONTWAIT);
	CHECK(n == -1 && errno == EAGAIN, "a batch without NLM_F_ACK answers nothing: %zd errno %d", n, errno);
	close(s);

	// A fresh socket dumps them.
	s = socket(AF_NETLINK, SOCK_RAW, NETLINK_NETFILTER);
	off = msg(0, NFNL_SUBSYS_NFTABLES << 8 | NFT_MSG_GETTABLE, NLM_F_DUMP | NLM_F_ACK, 7, NFPROTO_INET, 0, 0);
	CHECK(send(s, buf, off, 0) == off, "send the dump: errno %d", errno);
	// A second dump while the first runs.
	int off2 = msg(0, NFNL_SUBSYS_NFTABLES << 8 | NFT_MSG_GETTABLE, NLM_F_DUMP, 8, NFPROTO_INET, 0, 0);
	CHECK(send(s, buf, off2, 0) == off2, "send the second dump: errno %d", errno);
	int dgrams = 0, dumps = 0, tables = 0, done = 0, busy = 0, acked = 0;
	ssize_t first = 0, second = 0;
	while (!done) {
		n = recv(s, buf, 1 << 16, MSG_DONTWAIT);
		if (n <= 0)
			break;
		dgrams++;
		ssize_t size = n;
		int dump = 0;
		for (struct nlmsghdr *h = (struct nlmsghdr *)buf; NLMSG_OK(h, n); h = NLMSG_NEXT(h, n)) {
			if (h->nlmsg_type == NLMSG_DONE) {
				// The kernel learns the dump is over on the call after the
				// last entries: NLMSG_DONE comes alone.
				CHECK(size == NLMSG_LENGTH(sizeof(int)), "NLMSG_DONE comes in a datagram of its own: %zd", size);
				done = 1;
			} else if (h->nlmsg_type == NLMSG_ERROR && h->nlmsg_seq == 8 && ((struct nlmsgerr *)NLMSG_DATA(h))->error == -EBUSY) {
				busy = 1;
				// An error echoes the whole request (an ack only its header).
				CHECK(h->nlmsg_len == NLMSG_LENGTH(sizeof(int)) + (unsigned)off2, "the EBUSY error echoes the request: %u", h->nlmsg_len);
			} else if (h->nlmsg_type == NLMSG_ERROR) {
				acked = 1;
			} else if ((h->nlmsg_type & 0xff) == NFT_MSG_NEWTABLE) {
				tables++;
				dump = 1;
			}
		}
		if (dump && dumps++ == 0)
			first = size;
		else if (dump && dumps == 2)
			second = size;
	}
	CHECK(done && tables >= TABLES, "the dump ends: %d tables in %d datagrams", tables, dgrams);
	CHECK(first > 0 && first <= good, "the first dump datagram is at most NLMSG_GOODSIZE %d: %zd", good, first);
	CHECK(second > good, "after a 64 KiB receive the next dump datagram fills more: %zd", second);
	CHECK(busy, "a dump asked for while one runs fails with EBUSY");
	n = recv(s, buf, sizeof buf, MSG_DONTWAIT);
	CHECK(!acked && n == -1 && errno == EAGAIN, "a dump is not acked: acked %d, then %zd errno %d", acked, n, errno);
	printf("first %zd second %zd datagrams %d\nnldump fails=%d\n", first, second, dgrams, fails);
	return fails != 0;
}
