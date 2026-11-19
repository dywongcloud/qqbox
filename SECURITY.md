# Security Policy

qqbox runs untrusted Linux programs and OCI containers directly on the host CPU,
with a Go supervisor emulating the Linux ABI. Because it is a sandbox, a guest
that escapes the supervisor's mediation — reaching host memory, files, or the
network outside its configured policy, or corrupting the supervisor itself — is a
security issue. See the [Security model](README.md#security-model) for what
qqbox isolates and what it does not.

## Reporting a vulnerability

Please report privately through GitHub's **"Report a vulnerability"** button on
the repository's **Security** tab (private Security Advisories) — don't open a
public issue for a suspected isolation bug. We aim to acknowledge within a few
days and will coordinate a fix and disclosure with you.

Helpful details: the qqbox commit, the host OS and architecture, a minimal
reproduction (the program or image plus the flags used), and which isolation
boundary you believe is crossed.
