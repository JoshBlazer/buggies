# Sandbox host: WSL2 Ubuntu + Docker + gVisor

The sandbox executor talks to a Docker daemon and asks for the `runsc`
(gVisor) runtime. On this machine that daemon lives in WSL2 Ubuntu, not
Docker Desktop — gVisor is Linux-only.

Already in place: Docker (native, user in `docker` group), Go under
`~/.local/go` (user-local, on PATH via `~/.profile`).

One-time step that needs sudo (installs `runsc`, registers it with Docker):

    wsl -d Ubuntu -- sudo bash /mnt/c/Users/user/documents/buggies/deploy/wsl-gvisor-setup.sh

Run sandbox integration tests from WSL:

    wsl -d Ubuntu -- bash -lc 'cd /mnt/c/Users/user/documents/kernel && go test -tags integration ./sandbox/...'

If `runsc` is absent the executor refuses to run in `strict` mode and falls
back to hardened `runc` only when explicitly configured `allow-runc` — never
silently.
