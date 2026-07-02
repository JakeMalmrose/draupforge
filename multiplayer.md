# Multiplayer plan

What we're building, in one breath: named accounts, friends, parties — and the
game publicly hosted on the nuc. Kept deliberately short; details get decided
in the feature branches.

## The feature

1. **Identity.** On connect you pick a name — or hit "play as guest". Naming
   mints a random secret token the server maps to your character, handed back
   as an HttpOnly cookie. The name is how people see you; the token is the
   auth. Nobody can steal your character by knowing your name — they'd need
   the unguessable cookie.
2. **Guests.** No name, no cookie, no persistence — a throwaway character
   that dies with the session.
3. **Friends.** Named players are listed and invitable by default (an opt-out
   flag can come later). Friends list shows who's online.
4. **Parties.** A party gets its own world instance running exactly today's
   multiplayer: shared floors, shared run, shared portal uses. Solo players
   get their own instance. This is the real engineering lift — `server` goes
   from hosting one Instance to a lobby that routes connections to many.

Build order: identity + guests → per-party instances → friends/invites UI.

## Hosting (the nuc, no port forwarding needed)

Tailscale Funnel relays public HTTPS to the nuc through Tailscale's edge —
ISP said no to port forwarding, so this is the plan. Verified 2026-07-02:
nuc is Ubuntu 24.04 x86_64, Tailscale 1.98.4 (Funnel-capable), no serve
config yet. Public URL will be `https://nuc.tail4b8d48.ts.net`.

1. Client fix: `web/client.js` hardcodes `ws://` — make it protocol-relative
   (`wss:` on https pages) or browsers will block the socket.
2. Build on the mac: `GOOS=linux GOARCH=amd64 go build -o playserver ./cmd/server`.
3. Ship to the nuc (`scp` binary + `web/` + `scripts/arena.json`), run it with
   `-addr "" -admin ""` so only the HTTP/WS port exists (the TCP command port
   and the no-auth admin dashboard must never be public). systemd unit so it
   survives reboots.
4. `sudo tailscale funnel --bg 8080` on the nuc. First run may print an admin
   console link to click, enabling Funnel for the tailnet — one-time.

Jake-only steps: the sudo (or grant the user Tailscale operator rights) and
that one-time admin console click.
