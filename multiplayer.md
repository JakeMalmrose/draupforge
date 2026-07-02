# Multiplayer — design + hosting record

What we built, in one breath: named accounts, friends, parties — and the
game publicly hosted on the nuc. All shipped (sessions 36–39); this doc is
the design rationale and the ops record.

## The feature (shipped)

1. **Identity.** On connect you pick a name — or hit "play as guest". Naming
   mints a random secret token the server maps to your character, handed back
   as an HttpOnly cookie. The name is how people see you; the token is the
   auth. Nobody can steal your character by knowing your name — they'd need
   the unguessable cookie. (`server/identity.go`, `-identities` JSON store.)
2. **Guests.** No name, no cookie, no persistence — a throwaway character
   that dies with the session.
3. **Friends.** Named players are listed and invitable by default (an opt-out
   flag can come later). The F panel shows who's online.
4. **Parties.** A party gets its own world instance running exactly today's
   multiplayer: shared floors, shared run, shared portal uses. Solo players
   get their own instance. (`server/lobby.go`: party = instance, invites
   transfer via the floor-swap machinery, 60s empty-instance reap doubles as
   reconnect grace. `cmd/partybot` is a fake friend for solo testing.)

Wire history: identity landed as v16, parties as v17 — but the parallel gems
branch also claimed v16, so the merged protocol on main is **v18**.

## Hosting (the nuc, no port forwarding needed)

Tailscale Funnel relays public HTTPS to the nuc through Tailscale's edge —
ISP said no to port forwarding. Live at **https://nuc.tail4b8d48.ts.net**:
nuc is Ubuntu 24.04 x86_64, binary + `web/` + `scripts/` in `~/draupforge/`,
systemd unit `draupforge` running with `-addr "" -admin ""` so only the
HTTP/WS port exists (the TCP command port and the no-auth admin dashboard
must never be public), `tailscale funnel --bg 8080` in front.

The client picks `ws`/`wss` from the page protocol (hardcoded `ws://` would
be blocked on HTTPS pages).

## Deploys (CI/CD — never by hand)

Every push to `main` deploys via `.github/workflows/deploy.yml` on a
self-hosted runner on the nuc (`nuc-draupforge`):

- builds `cmd/server` with the Go version from `go.mod`,
- swaps the binary into `~/draupforge/` keeping `playserver.prev` for
  rollback,
- syncs `web/` and `scripts/` — **`identities.json` is never touched**,
- `sudo systemctl restart draupforge` (passwordless via
  `/etc/sudoers.d/draupforge-deploy`), then health-checks :8080.

So: merging to main IS deploying. Don't scp binaries around.
